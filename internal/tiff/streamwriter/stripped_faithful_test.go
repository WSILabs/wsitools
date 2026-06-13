package streamwriter_test

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
	"github.com/wsilabs/wsitools/internal/tiff/streamwriter"
)

// TestStrippedMultiStripPredictorJPEGTables is the WSILabs/wsitools#1 acceptance
// for the streamwriter: it pulls the faithful, multi-strip LZW+Predictor=2 label
// source from a real Aperio SVS via opentile's AssociatedSourceOf, re-emits it
// through the new multi-strip AddStripped (carrying Predictor 317 and, when
// present, JPEGTables 347), reopens the output, and asserts the emitted label
// decodes to the same pixels. The single-strip-only AddStripped that drops
// Predictor corrupts the label and fails this test.
func TestStrippedMultiStripPredictorJPEGTables(t *testing.T) {
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
	var spec streamwriter.StrippedSpec
	for _, a := range ss.Associated() {
		if a.Type() != "label" {
			continue
		}
		src, ok := ss.AssociatedSourceOf(a)
		if !ok {
			t.Fatal("no faithful source for label")
		}
		di, err := a.Decode(decoder.DecodeOptions{Format: decoder.PixelFormatRGB})
		if err != nil {
			t.Fatal(err)
		}
		wantPix = tightLabel(di)
		spec = streamwriter.StrippedSpec{
			Width:           uint32(a.Size().W),
			Height:          uint32(a.Size().H),
			RowsPerStrip:    uint32(src.RowsPerStrip),
			SamplesPerPixel: uint16(src.Samples),
			Photometric:     uint16(src.Photometric),
			Compression:     tiff.CompressionLZW,
			Strips:          src.Strips,
			Predictor:       uint16(src.Predictor),
			JPEGTables:      src.JPEGTables,
			WSIImageType:    "label",
			NewSubfileType:  1, // reduced → trailing Label page in the SVS classifier
		}
		break
	}
	if spec.Width == 0 {
		t.Skip("no label associated in fixture")
	}
	// Sanity: this fixture's label must be multi-strip + Predictor to exercise
	// the faithful path (otherwise the test would pass trivially).
	if len(spec.Strips) < 2 || spec.Predictor < 2 {
		t.Fatalf("fixture label not multi-strip/predictor (strips=%d predictor=%d); test would be vacuous",
			len(spec.Strips), spec.Predictor)
	}

	out := filepath.Join(t.TempDir(), "out.svs")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	desc := "Aperio Image Library v12.0.15\n" +
		"512x512 [0,0 512x512] (256x256) JPEG/RGB Q=80|AppMag = 40|MPP = 0.25|Filename = synth"

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

	w, err := streamwriter.Create(out, streamwriter.Options{
		BigTIFF:          tiff.BigTIFFOff,
		ImageDescription: desc,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Two tiled baseline levels so the label lands as a trailing reduced page
	// (page 1 non-tiled would be classified Thumbnail, not Label).
	mkLevel := func(W, H uint32) {
		l, err := w.AddLevel(streamwriter.LevelSpec{
			ImageWidth: W, ImageHeight: H,
			TileWidth: 256, TileHeight: 256,
			Compression:    tiff.CompressionJPEG,
			Photometric:    2,
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
	mkLevel(512, 512)
	mkLevel(256, 256)

	if err := w.AddStripped(spec); err != nil {
		t.Fatalf("AddStripped: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	gs, err := opentile.OpenFile(out)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer gs.Close()
	for _, a := range gs.Associated() {
		if a.Type() != "label" {
			continue
		}
		di, err := a.Decode(decoder.DecodeOptions{Format: decoder.PixelFormatRGB})
		if err != nil {
			t.Fatalf("decode emitted label: %v", err)
		}
		if !bytes.Equal(tightLabel(di), wantPix) {
			t.Error("emitted multi-strip label pixels differ (predictor/strips not faithful)")
		}
		return
	}
	t.Fatal("emitted file has no label associated")
}

func tightLabel(di *decoder.Image) []byte {
	rb := di.Width * 3
	o := make([]byte, di.Height*rb)
	for y := 0; y < di.Height; y++ {
		copy(o[y*rb:(y+1)*rb], di.Pix[y*di.Stride:y*di.Stride+rb])
	}
	return o
}
