package main

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	"math"
	"os"
	"runtime"
	"sync"
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
// The overview ("Label_Image") carries through the source's whole-slide
// overview/macro when present (oriented to portrait), else it is synthesized
// from the tissue (see buildBIFOverview). Re-encode runs on a worker pool
// (--workers / GOMAXPROCS). Limitations (Phase 1): no separate label/thumbnail
// or probability map carried; no --factor/--target-mag.
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
	var reencoders []*parallelReencodeSource
	defer func() {
		for _, r := range reencoders {
			r.Close()
		}
	}()
	for i, lvl := range levels {
		if lvl.Compression() != source.CompressionJPEG && !reencode {
			return fmt.Errorf("convert --to bif: source level %d is %s, not JPEG; "+
				"pass --codec jpeg to re-encode (BIF is a JPEG container)", lvl.Index(), lvl.Compression())
		}
		var ts bifwriter.TileSource
		if reencode {
			rs, err := newParallelReencodeSource(lvl, cvQuality, cvWorkers)
			if err != nil {
				return fmt.Errorf("jpeg re-encoder for level %d: %w", lvl.Index(), err)
			}
			reencoders = append(reencoders, rs)
			ts = rs
		} else {
			ts = bifwriter.FromLevel(lvl)
		}
		mag := baseMag
		if l0w > 0 && baseMag > 0 {
			mag = baseMag * float64(lvl.Size().X) / float64(l0w)
		}
		plevels[i] = bifwriter.PyramidLevel{Src: ts, Mag: mag}
	}

	ov, err := buildBIFOverview(src)
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
// bifOverviewW/H are the DP 200 canonical overview ("Label_Image") dimensions.
// All BIF fixtures' overviews are ~1:3 portrait (the whole 25×75 mm slide);
// they disagree on exact size (legacy iScan 1008×3008 JPEG, DP 200 1251×3685
// uncompressed) — per the convention to trust the DP 200 BIF, we emit 1251×3685.
const (
	bifOverviewW = 1251
	bifOverviewH = 3685
)

// buildBIFOverview produces the IFD-0 overview ("Label_Image", uncompressed RGB,
// 1251×3685 portrait). It CARRIES THROUGH a source whole-slide image when a good
// match exists — a source associated image of type "overview"/"macro", oriented
// to portrait and letterboxed onto the slide canvas — OTHERWISE it SYNTHESIZES a
// macro-style image: a white slide with the tissue (smallest pyramid level)
// placed in the bottom 2/3 (the tissue region; the top 1/3 is the blank label
// band the reader crops as the "label"). A tissue thumbnail is NOT used (it is
// not a whole-slide match), so it falls through to synthesis.
func buildBIFOverview(src source.Source) (bifwriter.Overview, error) {
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}

	// 1. Carry through a source whole-slide overview/macro.
	if a := pickOverviewAssoc(src); a != nil {
		if di, err := a.Decode(decoder.DecodeOptions{Format: decoder.PixelFormatRGB}); err == nil {
			img := decoderToRGBA(di)
			if img.Bounds().Dx() > img.Bounds().Dy() {
				img = rotate90CW(img) // landscape (e.g. Aperio macro) → portrait
			}
			return packOverview(fitTo(img, bifOverviewW, bifOverviewH, white)), nil
		}
		// decode failed → fall through to synthesis
	}

	// 2. Synthesize: white label band (top 1/3) + tissue (bottom 2/3).
	levels := src.Levels()
	tissue, err := decodeLevelToRGBA(levels[len(levels)-1], 2048)
	if err != nil {
		return bifwriter.Overview{}, fmt.Errorf("decode overview tissue: %w", err)
	}
	canvas := image.NewRGBA(image.Rect(0, 0, bifOverviewW, bifOverviewH))
	stddraw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: white}, image.Point{}, stddraw.Src)
	bandH := bifOverviewH / 3 // 25 mm label band of the 75 mm slide
	fitted := fitTo(tissue, bifOverviewW, bifOverviewH-bandH, white)
	stddraw.Draw(canvas, image.Rect(0, bandH, bifOverviewW, bifOverviewH), fitted, image.Point{}, stddraw.Src)
	return packOverview(canvas), nil
}

// pickOverviewAssoc returns the first source associated image that is a
// whole-slide overview/macro (not a tissue thumbnail), or nil.
func pickOverviewAssoc(src source.Source) source.AssociatedImage {
	for _, a := range src.Associated() {
		switch a.Type() {
		case "overview", "macro":
			return a
		}
	}
	return nil
}

func decoderToRGBA(di *decoder.Image) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, di.Width, di.Height))
	for y := 0; y < di.Height; y++ {
		srow := di.Pix[y*di.Stride:]
		drow := img.Pix[y*img.Stride:]
		for x := 0; x < di.Width; x++ {
			drow[x*4] = srow[x*3]
			drow[x*4+1] = srow[x*3+1]
			drow[x*4+2] = srow[x*3+2]
			drow[x*4+3] = 255
		}
	}
	return img
}

// rotate90CW rotates an RGBA image 90° clockwise (landscape → portrait, putting
// a left-edge label at the top, matching the BIF Label_Image convention).
func rotate90CW(s *image.RGBA) *image.RGBA {
	b := s.Bounds()
	w, h := b.Dx(), b.Dy()
	d := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			si := s.PixOffset(b.Min.X+x, b.Min.Y+y)
			dindex := d.PixOffset(h-1-y, x)
			copy(d.Pix[dindex:dindex+4], s.Pix[si:si+4])
		}
	}
	return d
}

// decodeLevelToRGBA decodes a pyramid level into an RGBA image, nearest-neighbour
// subsampled so its longest side is <= capDim (bounded memory).
func decodeLevelToRGBA(lvl source.Level, capDim int) (*image.RGBA, error) {
	sw, sh := lvl.Size().X, lvl.Size().Y
	tw, th := lvl.TileSize().X, lvl.TileSize().Y
	if sw <= 0 || sh <= 0 || tw <= 0 || th <= 0 {
		return nil, fmt.Errorf("degenerate level %dx%d tile %dx%d", sw, sh, tw, th)
	}
	cols := (sw + tw - 1) / tw
	rows := (sh + th - 1) / th
	scale := 1
	for sw/scale > capDim || sh/scale > capDim {
		scale *= 2
	}
	ow := (sw + scale - 1) / scale
	oh := (sh + scale - 1) / scale
	img := image.NewRGBA(image.Rect(0, 0, ow, oh))
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			di, err := lvl.DecodedTile(col, row)
			if err != nil {
				return nil, fmt.Errorf("decode tile (%d,%d): %w", col, row, err)
			}
			for y := 0; y < di.Height; y++ {
				gy := row*th + y
				if gy >= sh {
					break
				}
				oy := gy / scale
				if oy >= oh {
					continue
				}
				for x := 0; x < di.Width; x++ {
					gx := col*tw + x
					if gx >= sw {
						break
					}
					ox := gx / scale
					if ox >= ow {
						continue
					}
					si := y*di.Stride + x*3
					dindex := img.PixOffset(ox, oy)
					img.Pix[dindex] = di.Pix[si]
					img.Pix[dindex+1] = di.Pix[si+1]
					img.Pix[dindex+2] = di.Pix[si+2]
					img.Pix[dindex+3] = 255
				}
			}
		}
	}
	return img, nil
}

// packOverview converts an RGBA image to the packed RGB888 bifwriter.Overview.
func packOverview(img *image.RGBA) bifwriter.Overview {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	rgb := make([]byte, w*h*3)
	for y := 0; y < h; y++ {
		srow := img.Pix[y*img.Stride:]
		for x := 0; x < w; x++ {
			o := (y*w + x) * 3
			rgb[o] = srow[x*4]
			rgb[o+1] = srow[x*4+1]
			rgb[o+2] = srow[x*4+2]
		}
	}
	return bifwriter.Overview{W: w, H: h, RGB: rgb}
}

// standaloneJPEG is the jpeg encoder's self-contained-tile capability (a
// complete SOI+DQT+DHT+SOS+scan+EOI JPEG per tile, not the abbreviated
// shared-tables form). Real DP 200 tiles are self-contained, so BIF uses this.
type standaloneJPEG interface {
	EncodeStandalone(rgb []byte, w, h int) ([]byte, error)
	Close() error
}

var errReencodeClosed = errors.New("re-encode source closed")

// parallelReencodeSource is a bifwriter.TileSource that decodes + re-encodes a
// level's tiles to self-contained JPEG concurrently (one encoder per worker).
// It exploits WritePyramid's strict row-major TileInto order: workers run ahead
// up to a bounded window of the consumed index, so memory is capped (~window
// encoded tiles) regardless of level size, and CPU is fully utilised. Used under
// --codec jpeg for non-JPEG sources.
type parallelReencodeSource struct {
	lvl        source.Level
	tw, th     int
	cols, n    int

	mu       sync.Mutex
	cond     *sync.Cond
	ready    map[int][]byte // idx -> encoded bytes, awaiting TileInto
	err      error
	next     int // next tile index to dispatch (work-stealing)
	consumed int // count of tiles taken by TileInto (== next idx wanted)
	window   int

	encoders []standaloneJPEG
	wg       sync.WaitGroup
}

func newParallelReencodeSource(lvl source.Level, quality string, workers int) (*parallelReencodeSource, error) {
	tw, th := lvl.TileSize().X, lvl.TileSize().Y
	cols := (lvl.Size().X + tw - 1) / tw
	rows := (lvl.Size().Y + th - 1) / th
	if workers < 1 {
		workers = runtime.GOMAXPROCS(0)
	}
	s := &parallelReencodeSource{
		lvl: lvl, tw: tw, th: th, cols: cols, n: cols * rows,
		ready: make(map[int][]byte), window: 2*workers + 4,
	}
	s.cond = sync.NewCond(&s.mu)
	for i := 0; i < workers; i++ {
		enc, err := newBIFJPEGEncoder(tw, th, quality)
		if err != nil {
			s.Close()
			return nil, err
		}
		s.encoders = append(s.encoders, enc)
	}
	for _, enc := range s.encoders {
		s.wg.Add(1)
		go s.worker(enc)
	}
	return s, nil
}

func (s *parallelReencodeSource) SizeW() int       { return s.lvl.Size().X }
func (s *parallelReencodeSource) SizeH() int       { return s.lvl.Size().Y }
func (s *parallelReencodeSource) TileW() int       { return s.tw }
func (s *parallelReencodeSource) TileH() int       { return s.th }
func (s *parallelReencodeSource) TileMaxSize() int { return s.tw*s.th*3 + 4096 }

func (s *parallelReencodeSource) worker(enc standaloneJPEG) {
	defer s.wg.Done()
	for {
		s.mu.Lock()
		for s.err == nil && s.next < s.n && s.next >= s.consumed+s.window {
			s.cond.Wait() // window gate: don't run too far ahead of the writer
		}
		if s.err != nil || s.next >= s.n {
			s.mu.Unlock()
			return
		}
		idx := s.next
		s.next++
		s.mu.Unlock()

		col, row := idx%s.cols, idx/s.cols
		img, err := s.lvl.DecodedTile(col, row)
		var b []byte
		if err == nil {
			b, err = enc.EncodeStandalone(packTileRGB(img, s.tw, s.th), s.tw, s.th)
		}

		s.mu.Lock()
		if err != nil {
			if s.err == nil {
				s.err = fmt.Errorf("re-encode tile (%d,%d): %w", col, row, err)
			}
			s.cond.Broadcast()
			s.mu.Unlock()
			return
		}
		s.ready[idx] = b
		s.cond.Broadcast()
		s.mu.Unlock()
	}
}

func (s *parallelReencodeSource) TileInto(x, y int, dst []byte) (int, error) {
	idx := y*s.cols + x
	s.mu.Lock()
	for s.ready[idx] == nil && s.err == nil {
		s.cond.Wait()
	}
	if s.err != nil {
		s.mu.Unlock()
		return 0, s.err
	}
	b := s.ready[idx]
	delete(s.ready, idx)
	s.consumed++
	s.cond.Broadcast() // release window-gated workers
	s.mu.Unlock()

	if len(b) > len(dst) {
		return 0, fmt.Errorf("re-encoded tile (%d,%d) is %d bytes, exceeds buffer %d", x, y, len(b), len(dst))
	}
	return copy(dst, b), nil
}

// Close stops the workers (unblocking any that are window-gated) and releases
// the encoders. Safe to call after normal completion or on abort.
func (s *parallelReencodeSource) Close() error {
	s.mu.Lock()
	if s.err == nil {
		s.err = errReencodeClosed
	}
	s.cond.Broadcast()
	s.mu.Unlock()
	s.wg.Wait()
	for _, e := range s.encoders {
		e.Close()
	}
	return nil
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
