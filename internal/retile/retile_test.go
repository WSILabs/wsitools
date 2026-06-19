package retile

import (
	"bytes"
	"context"
	"image"
	stdjpeg "image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/decoder/all"
	_ "github.com/wsilabs/opentile-go/formats/all"
	"github.com/wsilabs/opentile-go/resample"
)

func testdir() string {
	if d := os.Getenv("WSI_TOOLS_TESTDIR"); d != "" {
		return d
	}
	return "../../sample_files"
}

// countingSink records, per engine level, the number of tiles written and that
// each body is a decodable JPEG.
type countingSink struct {
	mu       sync.Mutex
	perLevel map[int]int
	t        *testing.T
}

func (s *countingSink) WriteTile(level, col, row int, encoded []byte) error {
	if _, err := stdjpeg.Decode(bytes.NewReader(encoded)); err != nil {
		s.t.Errorf("L%d (%d,%d): body not a decodable JPEG: %v", level, col, row, err)
		return nil
	}
	s.mu.Lock()
	s.perLevel[level]++
	s.mu.Unlock()
	return nil
}

func TestRunEmitsFullOctavePyramid(t *testing.T) {
	path := filepath.Join(testdir(), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	slide, err := opentile.OpenFile(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer slide.Close()

	l0 := slide.Pyramids()[0].Levels[0]
	srcW, srcH := l0.Size.W, l0.Size.H
	const ts = 256
	levels := ComputeLevels(opentile.Size{W: srcW, H: srcH}, ts, ts, 1 /*overlap*/, 2 /*ratio*/, octaveCount(srcW, srcH))

	sink := &countingSink{perLevel: map[int]int{}, t: t}
	err = Run(context.Background(), Spec{
		Slide:     slide,
		SrcRegion: opentile.Region{Origin: opentile.Point{X: 0, Y: 0}, Size: opentile.Size{W: srcW, H: srcH}},
		OutL0:     opentile.Size{W: srcW, H: srcH},
		Levels:    levels,
		Kernel:    resample.Nearest, // identity scale (out == src)
		Encoder:   stubJPEG{},
		Sink:      sink,
		Workers:   4,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	wantFinest := levels[0].Cols * levels[0].Rows
	if sink.perLevel[0] != wantFinest {
		t.Errorf("finest level tiles = %d, want %d", sink.perLevel[0], wantFinest)
	}
	last := len(levels) - 1
	if sink.perLevel[last] != 1 {
		t.Errorf("coarsest level tiles = %d, want 1", sink.perLevel[last])
	}
}

// TestRunSurfacesEncodeErrorNotCancel proves Run returns the REAL encode error,
// not the context.Canceled it triggers itself via onEncErr→cancel (which makes
// ScaledStrips' Next() return context.Canceled as srcErr). Reuses the package's
// nthErrorEncoder (errors on the nth EncodeTile) and captureSink (accepts any
// body, so the failure path is the encoder — not the sink's JPEG-decode check).
func TestRunSurfacesEncodeErrorNotCancel(t *testing.T) {
	path := filepath.Join(testdir(), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	slide, err := opentile.OpenFile(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer slide.Close()

	l0 := slide.Pyramids()[0].Levels[0]
	srcW, srcH := l0.Size.W, l0.Size.H
	const ts = 256
	levels := ComputeLevels(opentile.Size{W: srcW, H: srcH}, ts, ts, 1 /*overlap*/, 2 /*ratio*/, octaveCount(srcW, srcH))

	sink := &captureSink{} // accepts everything, so the failure path is the encoder
	err = Run(context.Background(), Spec{
		Slide:     slide,
		SrcRegion: opentile.Region{Origin: opentile.Point{X: 0, Y: 0}, Size: opentile.Size{W: srcW, H: srcH}},
		OutL0:     opentile.Size{W: srcW, H: srcH},
		Levels:    levels,
		Kernel:    resample.Nearest,
		Encoder:   &nthErrorEncoder{n: 3}, // fail on the 3rd encoded tile
		Sink:      sink,
		Workers:   4,
	})
	if err == nil {
		t.Fatal("Run returned nil; want the encode error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("Run returned %q, want it to contain \"boom\" (encode error must not be masked by context.Canceled)", err.Error())
	}
}

// octaveCount returns the number of octave levels from native down to 1×1.
func octaveCount(w, h int) int {
	m := w
	if h > m {
		m = h
	}
	n := 1
	for m > 1 {
		m = (m + 1) / 2
		n++
	}
	return n
}

// stubJPEG produces a tiny valid JPEG via the stdlib encoder so the test needs
// no cgo. (The production path uses libjpeg-turbo via the driver's adapter.)
type stubJPEG struct{}

func (stubJPEG) EncodeTile(rgb []byte, w, h int) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w*3 + x*3
			o := img.PixOffset(x, y)
			img.Pix[o+0], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = rgb[i+0], rgb[i+1], rgb[i+2], 0xFF
		}
	}
	var b bytes.Buffer
	if err := stdjpeg.Encode(&b, img, &stdjpeg.Options{Quality: 80}); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
