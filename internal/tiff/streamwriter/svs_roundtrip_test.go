package streamwriter_test

import (
	"path/filepath"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	_ "github.com/wsilabs/opentile-go/formats/all"
	svsfmt "github.com/wsilabs/opentile-go/formats/svs"

	"github.com/cornish/wsitools/internal/codec"
	jpegcodec "github.com/cornish/wsitools/internal/codec/jpeg"
	"github.com/cornish/wsitools/internal/tiff"
	"github.com/cornish/wsitools/internal/tiff/streamwriter"
)

// TestSyntheticSVSRoundTrip writes a synthetic 2-level Aperio-shaped SVS using
// the JPEG codec wrapper, then re-opens it via opentile-go and asserts SVS
// detection + level count + tile readability.
func TestSyntheticSVSRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "synth.svs")

	desc := `Aperio Image Library v12.0.15
512x512 [0,0 512x512] (256x256) JPEG/RGB Q=80|AppMag = 40|MPP = 0.25|Filename = synth`

	enc, err := (jpegcodec.Factory{}).NewEncoder(codec.LevelGeometry{
		TileWidth: 256, TileHeight: 256, PixelFormat: codec.PixelFormatRGB8,
	}, codec.Quality{Knobs: map[string]string{"q": "80"}})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	defer enc.Close()
	tables := enc.LevelHeader()

	w, err := streamwriter.Create(path, streamwriter.Options{
		BigTIFF:          tiff.BigTIFFOff,
		ImageDescription: desc,
	})
	if err != nil {
		t.Fatal(err)
	}

	// All pyramid IFDs use NewSubfileType=0 — opentile-go's SVS classifier walks
	// pyramid levels as "tiled AND NOT reduced".
	tile := make([]byte, 256*256*3)
	encoded, err := enc.EncodeTile(tile, 256, 256, nil)
	if err != nil {
		t.Fatalf("EncodeTile: %v", err)
	}

	mkLevel := func(W, H uint32) {
		l, err := w.AddLevel(streamwriter.LevelSpec{
			ImageWidth: W, ImageHeight: H,
			TileWidth: 256, TileHeight: 256,
			Compression:    tiff.CompressionJPEG,
			Photometric:    2, // RGB (Aperio)
			JPEGTables:     tables,
			NewSubfileType: 0,
		})
		if err != nil {
			t.Fatal(err)
		}
		tx := (W + 255) / 256
		ty := (H + 255) / 256
		for y := uint32(0); y < ty; y++ {
			for x := uint32(0); x < tx; x++ {
				if err := l.WriteTile(x, y, encoded); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	mkLevel(512, 512) // L0: 4 tiles
	mkLevel(256, 256) // L1: 1 tile
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	tlr, err := opentile.OpenFile(path)
	if err != nil {
		t.Fatalf("opentile.OpenFile: %v", err)
	}
	defer tlr.Close()
	if got, want := string(tlr.Format()), "svs"; got != want {
		t.Errorf("Format: got %q, want %q", got, want)
	}
	if got, want := len(tlr.Levels()), 2; got != want {
		t.Errorf("Levels: got %d, want %d", got, want)
	}
	if md, ok := svsfmt.MetadataOf(tlr); ok {
		if md.MPP == 0 {
			t.Errorf("MPP zero in re-read metadata")
		}
	} else {
		t.Errorf("svs.MetadataOf returned !ok")
	}
	got, err := tlr.Levels()[0].Tile(0, 0)
	if err != nil {
		t.Fatalf("Tile(0,0): %v", err)
	}
	if len(got) == 0 {
		t.Errorf("empty tile bytes from opentile-go")
	}
}
