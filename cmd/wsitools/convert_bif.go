package main

import (
	"fmt"
	"math"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff/bifwriter"
)

// runConvertBIF writes a Ventana/Roche DP 200-shaped BIF from any
// opentile-readable source whose pyramid tiles are JPEG (BIF is a JPEG
// container). Tiles are copied verbatim; the full pyramid is emitted plus a
// generated whole-slide overview (the "Label_Image"). Single-AOI, no Z.
//
// Limitations (Phase 1): JPEG sources only (no re-encode); no source associated
// images carried; no probability map; --factor/--target-mag not yet supported.
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
	// BIF stores JPEG tiles; verbatim tile-copy requires a JPEG source. (No
	// silent codec assumptions — mirror the rest of `convert`.)
	for _, lvl := range levels {
		if lvl.Compression() != source.CompressionJPEG {
			return fmt.Errorf("convert --to bif: source level %d is %s, but BIF requires JPEG tiles; "+
				"re-encode to BIF is not yet supported (convert --to tiff --codec jpeg first, then --to bif)",
				lvl.Index(), lvl.Compression())
		}
	}

	md := src.Metadata()
	baseMag := md.Magnification
	mpp := md.MPPX
	if mpp == 0 {
		mpp = md.MPP
	}

	// Per-level magnification: baseMag scaled by each level's downsample.
	l0w := levels[0].Size().X
	plevels := make([]bifwriter.PyramidLevel, len(levels))
	for i, lvl := range levels {
		mag := baseMag
		if l0w > 0 && baseMag > 0 {
			mag = baseMag * float64(lvl.Size().X) / float64(l0w)
		}
		plevels[i] = bifwriter.PyramidLevel{Src: bifwriter.FromLevel(lvl), Mag: mag}
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
