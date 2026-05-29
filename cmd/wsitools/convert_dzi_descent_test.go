package main

import (
	"bytes"
	"context"
	"image"
	stdjpeg "image/jpeg"
	"sync"
	"testing"
	"time"

	"github.com/wsilabs/wsitools/internal/codec"
	"github.com/wsilabs/wsitools/internal/codec/jpeg"
	"github.com/wsilabs/wsitools/internal/dzi"
)

// makeRGB returns an *rgbImage of size w×h where pixel (x,y) =
// (id, byte(x), byte(y)) — encodes id+col+row for traceable
// assertions.
func makeRGB(w, h int, id byte) *rgbImage {
	img := &rgbImage{
		Pix:    make([]byte, w*h*3),
		Stride: w * 3,
		W:      w,
		H:      h,
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*img.Stride + x*3
			img.Pix[i+0] = id
			img.Pix[i+1] = byte(x)
			img.Pix[i+2] = byte(y)
		}
	}
	return img
}

func TestBoxDownsample2xHalvesDimensions(t *testing.T) {
	src := makeRGB(8, 8, 0xAA)
	dst := boxDownsample2x(src)
	if dst.W != 4 || dst.H != 4 {
		t.Errorf("dst dims: %dx%d, want 4x4", dst.W, dst.H)
	}
}

func TestBoxDownsample2xAverages2x2(t *testing.T) {
	src := &rgbImage{
		Pix:    make([]byte, 4*4*3),
		Stride: 4 * 3,
		W:      4,
		H:      4,
	}
	// Fill 4x4 with R=y*64, G=x*64, B=100.
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			i := y*src.Stride + x*3
			src.Pix[i+0] = byte(y * 64)
			src.Pix[i+1] = byte(x * 64)
			src.Pix[i+2] = 100
		}
	}
	dst := boxDownsample2x(src)
	if dst.W != 2 || dst.H != 2 {
		t.Fatalf("dst dims: %dx%d, want 2x2", dst.W, dst.H)
	}
	// dst(0,0) = average of src{(0,0),(1,0),(0,1),(1,1)}
	//   R: (0+0+64+64)/4 = 32
	//   G: (0+64+0+64)/4 = 32
	//   B: 100
	i := 0*dst.Stride + 0*3
	rr, gg, bb := dst.Pix[i+0], dst.Pix[i+1], dst.Pix[i+2]
	if rr != 32 || gg != 32 || bb != 100 {
		t.Errorf("dst(0,0) = R=%d G=%d B=%d; want R=32 G=32 B=100", rr, gg, bb)
	}
}

func TestLevelBuilderEmitsTilesForCompletedStrip(t *testing.T) {
	// Configure a 512×512 level at TileSize=256, Overlap=1.
	// cols=2 rows=2. We test the L_max builder with no child;
	// just verify it emits the right number of tiles after feed+flush.
	cfg := dzi.Config{
		Width: 512, Height: 512,
		Format: "jpeg", TileSize: 256, Overlap: 1,
	}
	jobs := make(chan encodeJob, 16)

	lb := &levelBuilder{
		level: 1, width: 512, cfg: cfg,
		cols: 2, rows: 2,
		jobs: jobs,
	}

	strip0 := makeRGB(512, 256, 0)
	strip1 := makeRGB(512, 256, 1)

	lb.feed(strip0)
	lb.feed(strip1)
	lb.flush()
	close(jobs)

	var emitted []encodeJob
	for j := range jobs {
		emitted = append(emitted, j)
	}
	if len(emitted) != 4 {
		t.Errorf("emitted %d tiles, want 4 (2 cols × 2 rows)", len(emitted))
	}
	rowsSeen := map[int]int{}
	for _, j := range emitted {
		rowsSeen[j.row]++
	}
	if rowsSeen[0] != 2 || rowsSeen[1] != 2 {
		t.Errorf("rows distribution: %v, want row 0×2 + row 1×2", rowsSeen)
	}
}

func TestLevelBuilderCascade(t *testing.T) {
	// L_max width 512, L_max-1 width 256, L_max-2 width 128.
	// TileSize=256, Overlap=0 for simplicity.
	cfg := dzi.Config{
		Width: 512, Height: 512,
		Format: "jpeg", TileSize: 256, Overlap: 0,
	}
	jobs := make(chan encodeJob, 32)

	coarsest := &levelBuilder{
		level: 0, width: 128, cfg: cfg,
		cols: 1, rows: 1,
		jobs: jobs,
	}
	mid := &levelBuilder{
		level: 1, width: 256, cfg: cfg,
		cols: 1, rows: 1,
		child: coarsest,
		jobs:  jobs,
	}
	top := &levelBuilder{
		level: 2, width: 512, cfg: cfg,
		cols: 2, rows: 2,
		child: mid,
		jobs:  jobs,
	}

	top.feed(makeRGB(512, 256, 1))
	top.feed(makeRGB(512, 256, 2))
	top.flush()
	close(jobs)

	counts := map[int]int{}
	for j := range jobs {
		counts[j.level]++
	}

	if counts[2] != 4 {
		t.Errorf("L2 tile count: %d, want 4", counts[2])
	}
	if counts[1] != 1 {
		t.Errorf("L1 tile count: %d, want 1", counts[1])
	}
	if counts[0] != 1 {
		t.Errorf("L0 tile count: %d, want 1", counts[0])
	}
}

func TestEncoderPoolProducesDecodableJPEGs(t *testing.T) {
	enc, err := jpeg.New(
		codec.LevelGeometry{TileWidth: 64, TileHeight: 64},
		codec.Quality{Knobs: map[string]string{"q": "85"}},
	)
	if err != nil {
		t.Fatalf("New jpeg: %v", err)
	}
	defer enc.Close()

	encodeJobs := make(chan encodeJob, 4)
	writeJobs := make(chan writeJob, 4)

	var wg sync.WaitGroup
	wg.Add(2)
	cfg := dzi.Config{Format: "jpeg", TileSize: 64}
	go func() {
		defer wg.Done()
		encoderWorker(context.Background(), encodeJobs, writeJobs, cfg, enc, 85)
	}()
	go func() {
		defer wg.Done()
		encoderWorker(context.Background(), encodeJobs, writeJobs, cfg, enc, 85)
	}()

	for i := 0; i < 4; i++ {
		img := makeRGB(64, 64, byte(i))
		encodeJobs <- encodeJob{level: 1, col: i, row: 0, img: img}
	}
	close(encodeJobs)

	go func() { wg.Wait(); close(writeJobs) }()

	received := map[int]bool{}
	for w := range writeJobs {
		img, err := stdjpeg.Decode(bytes.NewReader(w.body))
		if err != nil {
			t.Errorf("col %d: decode: %v", w.col, err)
			continue
		}
		if img.Bounds() != image.Rect(0, 0, 64, 64) {
			t.Errorf("col %d: dims %v, want 64x64", w.col, img.Bounds())
		}
		received[w.col] = true
	}

	for i := 0; i < 4; i++ {
		if !received[i] {
			t.Errorf("col %d: no writeJob received", i)
		}
	}
}

func TestLevelBuilderEmitRowRespectsContext(t *testing.T) {
	// Zero-buffer channel — any unconditional send blocks forever.
	// With cancel-aware emitRow (Task 2), the test completes.
	jobs := make(chan encodeJob)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before emitRow is called

	cfg := dzi.Config{
		Width: 512, Height: 256,
		Format: "jpeg", TileSize: 256, Overlap: 0,
	}
	lb := &levelBuilder{
		level: 1, width: 512, cfg: cfg,
		cols: 2, rows: 1,
		cur:  makeRGB(512, 256, 0),
		jobs: jobs,
		ctx:  ctx,
	}

	done := make(chan struct{})
	go func() {
		lb.emitRow(0)
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("emitRow blocked despite cancelled context")
	}

	select {
	case j := <-jobs:
		t.Errorf("unexpected encodeJob delivered: level=%d col=%d", j.level, j.col)
	default:
		// expected — no job
	}
}
