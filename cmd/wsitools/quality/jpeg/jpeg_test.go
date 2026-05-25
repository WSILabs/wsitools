package jpeg

import (
	"bytes"
	"encoding/binary"
	"image"
	stdjpeg "image/jpeg"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/wsitools/cmd/wsitools/quality"
)

// encodeJPEG encodes a 16x16 YCbCr 4:2:0 image at quality q using
// Go's standard library JPEG encoder (always 4:2:0).
func encodeJPEG(t *testing.T, q int) []byte {
	t.Helper()
	img := image.NewYCbCr(image.Rect(0, 0, 16, 16), image.YCbCrSubsampleRatio420)
	for i := range img.Y {
		img.Y[i] = 128
	}
	var buf bytes.Buffer
	if err := stdjpeg.Encode(&buf, img, &stdjpeg.Options{Quality: q}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

// buildJPEGWithSampling builds a minimal JPEG with a hand-crafted SOF0
// segment specifying the given Y horizontal/vertical sampling factors.
// Cb and Cr always use 1,1. This allows testing 4:4:4 (Y H=1,V=1),
// 4:2:2 (Y H=2,V=1), and 4:2:0 (Y H=2,V=2) without needing a
// full JPEG encoder that supports subsampling control.
func buildJPEGWithSampling(t *testing.T, yH, yV byte) []byte {
	t.Helper()
	// Build a minimal JPEG: SOI + DQT + SOF0 + SOS (truncated is fine;
	// the inspector stops at SOS).

	var b bytes.Buffer

	// SOI
	b.Write([]byte{0xFF, 0xD8})

	// DQT (Define Quantization Table): 8-bit luma table (all 16s → Q≈84).
	// Format: 0xFF 0xDB, length (2+1+64=67), precision|id=0x00, 64 bytes.
	dqtPayload := make([]byte, 65) // 1 byte precision+id + 64 byte table
	dqtPayload[0] = 0x00           // 8-bit precision, table id 0
	for i := 1; i <= 64; i++ {
		dqtPayload[i] = 16 // uniform value
	}
	b.Write([]byte{0xFF, 0xDB})
	writeUint16BE(&b, uint16(2+len(dqtPayload)))
	b.Write(dqtPayload)

	// SOF0 (Baseline DCT): 3 components.
	// Length = 2 + 1 + 2 + 2 + 1 + 3*3 = 17
	sofPayload := []byte{
		8,            // precision
		0x00, 0x10,   // height = 16
		0x00, 0x10,   // width  = 16
		3,            // number of components
		1, (yH<<4 | yV), 0, // Y: id=1, H/V sampling, quant table 0
		2, 0x11, 0,         // Cb: id=2, 1x1 sampling, quant table 0
		3, 0x11, 0,         // Cr: id=3, 1x1 sampling, quant table 0
	}
	b.Write([]byte{0xFF, 0xC0})
	writeUint16BE(&b, uint16(2+len(sofPayload)))
	b.Write(sofPayload)

	// SOS — inspector stops here; no actual scan data needed.
	b.Write([]byte{0xFF, 0xDA})

	return b.Bytes()
}

func writeUint16BE(b *bytes.Buffer, v uint16) {
	var buf [2]byte
	binary.BigEndian.PutUint16(buf[:], v)
	b.Write(buf[:])
}

func TestJPEGInspectorRegistered(t *testing.T) {
	insp, ok := quality.For(opentile.CompressionJPEG)
	if !ok {
		t.Fatalf("JPEG inspector not registered")
	}
	if insp.Compression() != opentile.CompressionJPEG {
		t.Errorf("Compression(): got %v, want JPEG", insp.Compression())
	}
}

func TestInspectJPEGQualityEstimate(t *testing.T) {
	cases := []int{50, 75, 90}
	insp := &inspector{}
	for _, q := range cases {
		b := encodeJPEG(t, q)
		info, err := insp.Inspect(b)
		if err != nil {
			t.Fatalf("Inspect(Q=%d): %v", q, err)
		}
		// Allow ±10 since the standard libjpeg table → Q mapping
		// isn't an exact inverse.
		if info.QualityEstimate < q-10 || info.QualityEstimate > q+10 {
			t.Errorf("Q=%d: estimate %d outside [%d, %d]", q, info.QualityEstimate, q-10, q+10)
		}
		if info.Codec != "JPEG" {
			t.Errorf("Codec: got %q, want \"JPEG\"", info.Codec)
		}
		if info.Lossless {
			t.Errorf("Lossless should be false for JPEG")
		}
	}
}

func TestInspectJPEGChromaSubsampling(t *testing.T) {
	cases := []struct {
		yH, yV byte
		want   string
	}{
		{1, 1, "4:4:4"},
		{2, 1, "4:2:2"},
		{2, 2, "4:2:0"},
	}
	insp := &inspector{}
	for _, c := range cases {
		b := buildJPEGWithSampling(t, c.yH, c.yV)
		info, err := insp.Inspect(b)
		if err != nil {
			t.Fatalf("Inspect(%s): %v", c.want, err)
		}
		if info.ChromaSubsampling != c.want {
			t.Errorf("sampling Y H=%d,V=%d: got %q, want %q", c.yH, c.yV, info.ChromaSubsampling, c.want)
		}
	}
}

func TestInspectJPEGTruncated(t *testing.T) {
	// JPEG SOI only (no DQT/SOF) — should error.
	insp := &inspector{}
	_, err := insp.Inspect([]byte{0xFF, 0xD8})
	if err == nil {
		t.Error("expected error on truncated JPEG, got nil")
	}
}

func TestInspectJPEGNonJPEG(t *testing.T) {
	insp := &inspector{}
	_, err := insp.Inspect([]byte("Not a JPEG"))
	if err == nil {
		t.Error("expected error on non-JPEG bytes, got nil")
	}
}
