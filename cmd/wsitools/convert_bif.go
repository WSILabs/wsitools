package main

import (
	"fmt"
	"math"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/wsilabs/opentile-go/decoder"
	"github.com/wsilabs/wsitools/internal/codec"
	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff/bifwriter"
)

// runConvertBIF writes a Ventana/Roche DP 200-shaped BIF from any
// opentile-readable source. The full pyramid is emitted (row-major level=N
// IFDs) plus a generated whole-slide overview ("Label_Image"). JPEG sources are
// tile-copied verbatim; non-JPEG sources require --codec jpeg, which decodes and
// re-encodes each tile to JPEG (the BIF codec). Single-AOI, no Z.
//
// Limitations (Phase 1): re-encode is serial (slow for large non-JPEG slides);
// no source associated images carried; no probability map;
// --factor/--target-mag not yet supported.
func runConvertBIF(cmd *cobra.Command, input string, start time.Time) error {
	if cvFactor != 1 || cvTargetMag != 0 {
		return fmt.Errorf("--factor/--target-mag is not yet supported for --to bif")
	}
	if _, err := os.Stat(input); err != nil {
		return fmt.Errorf("input %s: %w", input, err)
	}
	if !cvForce {
		if _, err := os.Stat(cvOutput); err == nil {
			return fmt.Errorf("output %s already exists (use --force)", cvOutput)
		}
	}

	src, err := source.Open(input)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	levels := src.Levels()
	if len(levels) == 0 {
		return fmt.Errorf("source has no pyramid levels")
	}
	// BIF is a JPEG container. Verbatim tile-copy when the source is JPEG;
	// --codec jpeg re-encodes (handles non-JPEG sources, and forces re-encode of
	// JPEG sources). No other codec is valid — no silent codec assumptions.
	if cvCodec != "" && cvCodec != "jpeg" {
		return fmt.Errorf("convert --to bif: --codec %q not supported; BIF is a JPEG container (only --codec jpeg)", cvCodec)
	}
	reencode := cvCodec == "jpeg"

	md := src.Metadata()
	baseMag := md.Magnification
	mpp := md.MPPX
	if mpp == 0 {
		mpp = md.MPP
	}

	// Per-level magnification (baseMag scaled by each level's downsample) +
	// per-level TileSource (verbatim copy or JPEG re-encode).
	l0w := levels[0].Size().X
	plevels := make([]bifwriter.PyramidLevel, len(levels))
	var encoders []standaloneJPEG
	defer func() {
		for _, e := range encoders {
			e.Close()
		}
	}()
	for i, lvl := range levels {
		if lvl.Compression() != source.CompressionJPEG && !reencode {
			return fmt.Errorf("convert --to bif: source level %d is %s, not JPEG; "+
				"pass --codec jpeg to re-encode (BIF is a JPEG container)", lvl.Index(), lvl.Compression())
		}
		var ts bifwriter.TileSource
		if reencode {
			enc, err := newBIFJPEGEncoder(lvl.TileSize().X, lvl.TileSize().Y, cvQuality)
			if err != nil {
				return fmt.Errorf("jpeg encoder for level %d: %w", lvl.Index(), err)
			}
			encoders = append(encoders, enc)
			ts = reencodeSource{lvl: lvl, tw: lvl.TileSize().X, th: lvl.TileSize().Y, enc: enc}
		} else {
			ts = bifwriter.FromLevel(lvl)
		}
		mag := baseMag
		if l0w > 0 && baseMag > 0 {
			mag = baseMag * float64(lvl.Size().X) / float64(l0w)
		}
		plevels[i] = bifwriter.PyramidLevel{Src: ts, Mag: mag}
	}

	ov, err := buildBIFOverview(levels[len(levels)-1])
	if err != nil {
		return fmt.Errorf("build overview: %w", err)
	}

	magInt := int(math.Round(baseMag))
	if magInt == 0 {
		magInt = 40
	}
	meta := bifwriter.IScanMeta{Magnification: magInt, ScanRes: mpp}

	// Atomic write: temp → fsync → rename.
	tmp := cvOutput + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	if err := bifwriter.WritePyramid(f, plevels, ov, meta); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write bif: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, cvOutput); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename to %s: %w", cvOutput, err)
	}

	if !flagQuiet {
		fmt.Fprintf(cmd.OutOrStdout(), "wrote %s (bif, %d levels) in %s\n",
			cvOutput, len(levels), time.Since(start).Round(time.Millisecond))
	}
	return nil
}

// buildBIFOverview generates the whole-slide overview (Label_Image) from a
// source level, nearest-neighbour subsampled so its longest side is <= 2048 px
// (bounded memory: one decoded tile + the output buffer). Returns packed RGB888.
func buildBIFOverview(lvl source.Level) (bifwriter.Overview, error) {
	sw, sh := lvl.Size().X, lvl.Size().Y
	tw, th := lvl.TileSize().X, lvl.TileSize().Y
	if sw <= 0 || sh <= 0 || tw <= 0 || th <= 0 {
		return bifwriter.Overview{}, fmt.Errorf("degenerate overview source level %dx%d tile %dx%d", sw, sh, tw, th)
	}
	cols := (sw + tw - 1) / tw
	rows := (sh + th - 1) / th

	const capDim = 2048
	scale := 1
	for sw/scale > capDim || sh/scale > capDim {
		scale *= 2
	}
	ow := (sw + scale - 1) / scale
	oh := (sh + scale - 1) / scale
	rgb := make([]byte, ow*oh*3)

	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			img, err := lvl.DecodedTile(col, row)
			if err != nil {
				return bifwriter.Overview{}, fmt.Errorf("decode tile (%d,%d): %w", col, row, err)
			}
			for y := 0; y < img.Height; y++ {
				gy := row*th + y
				if gy >= sh {
					break
				}
				oy := gy / scale
				if oy >= oh {
					continue
				}
				for x := 0; x < img.Width; x++ {
					gx := col*tw + x
					if gx >= sw {
						break
					}
					ox := gx / scale
					if ox >= ow {
						continue
					}
					si := y*img.Stride + x*3
					di := (oy*ow + ox) * 3
					rgb[di] = img.Pix[si]
					rgb[di+1] = img.Pix[si+1]
					rgb[di+2] = img.Pix[si+2]
				}
			}
		}
	}
	return bifwriter.Overview{W: ow, H: oh, RGB: rgb}, nil
}

// standaloneJPEG is the jpeg encoder's self-contained-tile capability (a
// complete SOI+DQT+DHT+SOS+scan+EOI JPEG per tile, not the abbreviated
// shared-tables form). Real DP 200 tiles are self-contained, so BIF uses this.
type standaloneJPEG interface {
	EncodeStandalone(rgb []byte, w, h int) ([]byte, error)
	Close() error
}

// reencodeSource adapts a source.Level into a bifwriter.TileSource that decodes
// each tile and re-encodes it to a self-contained JPEG (the BIF codec) — used
// under --codec jpeg for non-JPEG sources. NOTE: serial decode+encode
// (WritePyramid reads tiles in order); slow for large slides — a worker pool is
// a later optimization.
type reencodeSource struct {
	lvl    source.Level
	tw, th int
	enc    standaloneJPEG
}

func (s reencodeSource) SizeW() int       { return s.lvl.Size().X }
func (s reencodeSource) SizeH() int       { return s.lvl.Size().Y }
func (s reencodeSource) TileW() int       { return s.tw }
func (s reencodeSource) TileH() int       { return s.th }
func (s reencodeSource) TileMaxSize() int { return s.tw*s.th*3 + 4096 }

func (s reencodeSource) TileInto(x, y int, dst []byte) (int, error) {
	img, err := s.lvl.DecodedTile(x, y)
	if err != nil {
		return 0, fmt.Errorf("decode tile (%d,%d): %w", x, y, err)
	}
	rgb := packTileRGB(img, s.tw, s.th)
	out, err := s.enc.EncodeStandalone(rgb, s.tw, s.th)
	if err != nil {
		return 0, fmt.Errorf("encode tile (%d,%d): %w", x, y, err)
	}
	if len(out) > len(dst) {
		return 0, fmt.Errorf("re-encoded tile (%d,%d) is %d bytes, exceeds buffer %d", x, y, len(out), len(dst))
	}
	return copy(dst, out), nil
}

// packTileRGB packs a decoded tile into a tw×th tightly-packed RGB888 buffer,
// zero-padding edge tiles to the full tile size (BIF stores full-size tiles).
func packTileRGB(img *decoder.Image, tw, th int) []byte {
	rgb := make([]byte, tw*th*3)
	for y := 0; y < th && y < img.Height; y++ {
		w := tw
		if img.Width < w {
			w = img.Width
		}
		copy(rgb[y*tw*3:y*tw*3+w*3], img.Pix[y*img.Stride:y*img.Stride+w*3])
	}
	return rgb
}

func newBIFJPEGEncoder(tw, th int, quality string) (standaloneJPEG, error) {
	fac, err := codec.Lookup("jpeg")
	if err != nil {
		return nil, err
	}
	knobs, err := parseQualityKnobs(quality)
	if err != nil {
		return nil, err
	}
	enc, err := fac.NewEncoder(codec.LevelGeometry{
		TileWidth:   tw,
		TileHeight:  th,
		PixelFormat: codec.PixelFormatRGB8,
	}, codec.Quality{Knobs: knobs})
	if err != nil {
		return nil, err
	}
	sa, ok := enc.(standaloneJPEG)
	if !ok {
		enc.Close()
		return nil, fmt.Errorf("jpeg encoder does not support self-contained tiles")
	}
	return sa, nil
}
