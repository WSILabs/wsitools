package streamwriter_test

import (
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	xtiff "golang.org/x/image/tiff"

	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
)

func TestCreateAndCloseEmpty(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.tiff")
	w, err := streamwriter.Create(out, streamwriter.Options{ToolsVersion: "test"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer w.Abort()
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("output missing after Close: %v", err)
	}
}

func TestAddLevelAndWriteTile(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.tiff")
	w, err := streamwriter.Create(out, streamwriter.Options{ToolsVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Abort()

	h, err := w.AddLevel(streamwriter.LevelSpec{
		ImageWidth: 8, ImageHeight: 8,
		TileWidth: 8, TileHeight: 8,
		BitsPerSample: []uint16{8, 8, 8}, SamplesPerPixel: 3,
		Photometric: 2, Compression: tiff.CompressionNone,
		NewSubfileType: 0, WSIImageType: tiff.WSIImageTypePyramid,
	})
	if err != nil {
		t.Fatalf("AddLevel: %v", err)
	}
	if err := h.WriteTile(0, 0, []byte("xxxxxxxx")); err != nil {
		t.Fatalf("WriteTile: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestAddStripped_Smoke(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.tiff")
	w, err := streamwriter.Create(out, streamwriter.Options{ToolsVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Abort()

	h, _ := w.AddLevel(streamwriter.LevelSpec{
		ImageWidth: 8, ImageHeight: 8, TileWidth: 8, TileHeight: 8,
		BitsPerSample: []uint16{8, 8, 8}, SamplesPerPixel: 3,
		Photometric: 2, Compression: tiff.CompressionNone,
		WSIImageType: tiff.WSIImageTypePyramid,
	})
	h.WriteTile(0, 0, []byte("xxxxxxxx"))

	if err := w.AddStripped(streamwriter.StrippedSpec{
		Width: 100, Height: 100, RowsPerStrip: 100,
		BitsPerSample: []uint16{8, 8, 8}, SamplesPerPixel: 3,
		Photometric: 2, Compression: tiff.CompressionNone,
		StripBytes:     make([]byte, 30000),
		NewSubfileType: 1, WSIImageType: tiff.WSIImageTypeLabel,
	}); err != nil {
		t.Fatalf("AddStripped: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestWSIImageTypeValidation(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.tiff")
	w, _ := streamwriter.Create(out, streamwriter.Options{ToolsVersion: "test"})
	defer w.Abort()
	_, err := w.AddLevel(streamwriter.LevelSpec{
		ImageWidth: 8, ImageHeight: 8, TileWidth: 8, TileHeight: 8,
		BitsPerSample: []uint16{8, 8, 8}, SamplesPerPixel: 3,
		Photometric: 2, Compression: tiff.CompressionNone,
		WSIImageType: "not-a-real-kind",
	})
	if err == nil {
		t.Errorf("expected validation error for bad WSIImageType")
	}
}

func TestAtomicClose_RemovesTmpOnAbort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.tiff")

	w, err := streamwriter.Create(path, streamwriter.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Abort(); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	// .tmp file should be gone; final file should not exist.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf(".tmp not removed: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("final path exists: %v", err)
	}
}

func TestAtomicClose_RenamesOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.tiff")
	w, _ := streamwriter.Create(path, streamwriter.Options{})
	level, _ := w.AddLevel(streamwriter.LevelSpec{
		ImageWidth: 8, ImageHeight: 8,
		TileWidth: 8, TileHeight: 8,
		Compression: tiff.CompressionNone, Photometric: 2,
	})
	level.WriteTile(0, 0, make([]byte, 8*8*3))
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("final path missing: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf(".tmp still present: %v", err)
	}
}

// TestWriteTiledTIFF writes a 16x16 RGB image laid out as four 8x8 uncompressed
// tiles, then re-decodes via golang.org/x/image/tiff to verify structural validity.
func TestWriteTiledTIFF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tiled.tiff")

	w, err := streamwriter.Create(path, streamwriter.Options{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	level, err := w.AddLevel(streamwriter.LevelSpec{
		ImageWidth:  16,
		ImageHeight: 16,
		TileWidth:   8,
		TileHeight:  8,
		Compression: tiff.CompressionNone,
		Photometric: 2, // RGB
	})
	if err != nil {
		t.Fatalf("AddLevel: %v", err)
	}

	for ty := uint32(0); ty < 2; ty++ {
		for tx := uint32(0); tx < 2; tx++ {
			tile := make([]byte, 8*8*3)
			for i := range tile {
				tile[i] = byte(ty*2 + tx + 1)
			}
			if err := level.WriteTile(tx, ty, tile); err != nil {
				t.Fatalf("WriteTile(%d,%d): %v", tx, ty, err)
			}
		}
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	img, err := xtiff.Decode(f)
	if err != nil {
		t.Fatalf("xtiff.Decode: %v", err)
	}
	if got, want := img.Bounds(), image.Rect(0, 0, 16, 16); got != want {
		t.Errorf("bounds: got %v, want %v", got, want)
	}
	r0, g0, b0, _ := img.At(0, 0).RGBA()
	r1, g1, b1, _ := img.At(8, 8).RGBA()
	if r0 == r1 && g0 == g1 && b0 == b1 {
		t.Errorf("tile (0,0) and (1,1) have identical pixels — tile layout wrong")
	}
}

// TestWriteBigTIFF writes a 16x16 RGB tiled BigTIFF and validates it using
// tiffinfo. Skipped if tiffinfo is not in PATH.
func TestWriteBigTIFF(t *testing.T) {
	if _, err := exec.LookPath("tiffinfo"); err != nil {
		t.Skip("tiffinfo not in PATH (brew install libtiff); skipping")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "big.tiff")

	w, err := streamwriter.Create(path, streamwriter.Options{BigTIFF: tiff.BigTIFFOn})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	level, err := w.AddLevel(streamwriter.LevelSpec{
		ImageWidth: 16, ImageHeight: 16,
		TileWidth: 8, TileHeight: 8,
		Compression: tiff.CompressionNone, Photometric: 2,
	})
	if err != nil {
		t.Fatalf("AddLevel: %v", err)
	}
	for ty := uint32(0); ty < 2; ty++ {
		for tx := uint32(0); tx < 2; tx++ {
			if err := level.WriteTile(tx, ty, make([]byte, 8*8*3)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command("tiffinfo", path).CombinedOutput()
	if err != nil {
		t.Fatalf("tiffinfo: %v\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "BigTIFF") && !strings.Contains(got, "Subfile") {
		t.Errorf("tiffinfo output doesn't mention BigTIFF or expected fields:\n%s", got)
	}
}

// TestWriteMinimalTIFF writes a minimal 8x8 RGB single-strip TIFF and verifies
// it via golang.org/x/image/tiff.
func TestWriteMinimalTIFF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "minimal.tiff")

	w, err := streamwriter.Create(path, streamwriter.Options{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	rgb := make([]byte, 8*8*3)
	if err := w.AddStripped(streamwriter.StrippedSpec{
		Width:       8,
		Height:      8,
		Compression: tiff.CompressionNone,
		Photometric: 2,
		StripBytes:  rgb,
	}); err != nil {
		t.Fatalf("AddStripped: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	img, err := xtiff.Decode(f)
	if err != nil {
		t.Fatalf("xtiff.Decode: %v", err)
	}
	if got, want := img.Bounds(), image.Rect(0, 0, 8, 8); got != want {
		t.Errorf("bounds: got %v, want %v", got, want)
	}
}

func TestWriterOptions_StandardMetadata(t *testing.T) {
	if _, err := exec.LookPath("tiffinfo"); err != nil {
		t.Skip("tiffinfo missing")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "with-md.tiff")
	when := time.Date(2026, 1, 15, 13, 14, 15, 0, time.UTC)

	w, err := streamwriter.Create(path, streamwriter.Options{
		BigTIFF:      tiff.BigTIFFOn,
		Make:         "Hamamatsu",
		Model:        "C9600",
		Software:     "wsi-tools/0.2.0-dev",
		DateTime:     when,
		SourceFormat: "philips-tiff",
		ToolsVersion: "0.2.0-dev",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	level, _ := w.AddLevel(streamwriter.LevelSpec{
		ImageWidth: 8, ImageHeight: 8, TileWidth: 8, TileHeight: 8,
		Compression: tiff.CompressionNone, Photometric: 2,
	})
	level.WriteTile(0, 0, make([]byte, 8*8*3))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command("tiffinfo", path).CombinedOutput()
	if err != nil {
		t.Fatalf("tiffinfo: %v\n%s", err, out)
	}
	got := string(out)
	for _, want := range []string{"Hamamatsu", "C9600", "wsi-tools/0.2.0-dev", "2026:01:15 13:14:15"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in tiffinfo output, got:\n%s", want, got)
		}
	}
}
