package cogwsiwriter_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/opentile-go/decoder"
	_ "github.com/wsilabs/opentile-go/formats/all"

	"github.com/wsilabs/wsitools/internal/codec"
	jpegcodec "github.com/wsilabs/wsitools/internal/codec/jpeg"
	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/cogwsiwriter"
)

// TestAssociatedMultiStripPredictorJPEGTables is the WSILabs/wsitools#1
// acceptance for the COG-WSI writer: it pulls the faithful, multi-strip
// LZW+Predictor=2 label source from a real Aperio SVS via opentile's
// AssociatedSourceOf, re-emits it through AddAssociated (carrying Predictor 317
// and, when present, JPEGTables 347), reopens the output, and asserts the
// emitted label decodes to the same pixels. The single-strip-only path that
// drops Predictor corrupts the label and fails this test.
func TestAssociatedMultiStripPredictorJPEGTables(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../../sample_files"
	}
	svs := filepath.Join(dir, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(svs); err != nil {
		t.Skip("no CMU fixture")
	}

	ss, err := opentile.OpenFile(svs)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	var wantPix []byte
	var spec cogwsiwriter.AssociatedSpec
	var found bool
	for _, a := range ss.AssociatedImages() {
		if a.Type() != "label" {
			continue
		}
		src, ok := a.Encoding()
		if !ok {
			t.Fatal("no faithful source for label")
		}
		di, err := a.Decode(decoder.DecodeOptions{Format: decoder.PixelFormatRGB})
		if err != nil {
			t.Fatal(err)
		}
		wantPix = tightImage(di)
		// Sanity: this fixture's label must be multi-strip + Predictor to
		// exercise the faithful path (otherwise the test would pass trivially).
		if len(src.Strips) < 2 || src.Predictor < 2 {
			t.Fatalf("fixture label not multi-strip/predictor (strips=%d predictor=%d); test would be vacuous",
				len(src.Strips), src.Predictor)
		}
		spec = cogwsiwriter.AssociatedSpec{
			Type:            "label",
			Width:           uint32(a.Size().W),
			Height:          uint32(a.Size().H),
			Compression:     tiff.CompressionLZW,
			Photometric:     uint16(src.Photometric),
			SamplesPerPixel: uint16(src.Samples),
			BitsPerSample:   []uint16{8, 8, 8},
			Strips:          src.Strips,
			Predictor:       uint16(src.Predictor),
			JPEGTables:      src.JPEGTables,
			RowsPerStrip:    uint32(src.RowsPerStrip),
		}
		found = true
		break
	}
	if !found {
		t.Skip("no label associated in fixture")
	}

	out := filepath.Join(t.TempDir(), "out.tiff")
	w, err := cogwsiwriter.Create(out, cogwsiwriter.Options{ToolsVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}

	// Minimal single-tile L0 so the file is a valid pyramid.
	enc, err := (jpegcodec.Factory{}).NewEncoder(codec.LevelGeometry{
		TileWidth: 256, TileHeight: 256, PixelFormat: codec.PixelFormatRGB8,
	}, codec.Quality{Knobs: map[string]string{"q": "80"}})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	defer enc.Close()
	tables := enc.LevelHeader()
	tile := make([]byte, 256*256*3)
	encoded, err := enc.EncodeTile(tile, 256, 256, nil)
	if err != nil {
		t.Fatalf("EncodeTile: %v", err)
	}

	l, err := w.AddLevel(cogwsiwriter.LevelSpec{
		ImageWidth: 256, ImageHeight: 256,
		TileWidth: 256, TileHeight: 256,
		Compression:     tiff.CompressionJPEG,
		Photometric:     2,
		SamplesPerPixel: 3,
		BitsPerSample:   []uint16{8, 8, 8},
		JPEGTables:      tables,
		IsL0:            true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.WriteTile(0, 0, encoded); err != nil {
		t.Fatal(err)
	}

	if err := w.AddAssociated(spec); err != nil {
		t.Fatalf("AddAssociated: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	gs, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer gs.Close()
	for _, a := range gs.AssociatedImages() {
		if a.Type() != "label" {
			continue
		}
		di, err := a.Decode(decoder.DecodeOptions{Format: decoder.PixelFormatRGB})
		if err != nil {
			t.Fatalf("decode emitted label: %v", err)
		}
		if !bytes.Equal(tightImage(di), wantPix) {
			t.Error("emitted multi-strip label pixels differ (predictor/strips not faithful)")
		}
		return
	}
	t.Fatal("emitted file has no label associated")
}

func tightImage(di *decoder.Image) []byte {
	rb := di.Width * 3
	o := make([]byte, di.Height*rb)
	for y := 0; y < di.Height; y++ {
		copy(o[y*rb:(y+1)*rb], di.Pix[y*di.Stride:y*di.Stride+rb])
	}
	return o
}
