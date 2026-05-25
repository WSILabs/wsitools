package webp

import (
	"bytes"
	"encoding/binary"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/wsitools/cmd/wsitools/quality"
)

// buildWebPVP8L builds a minimal valid-looking RIFF/WEBP container
// with a VP8L (lossless) chunk.
func buildWebPVP8L() []byte {
	var b bytes.Buffer
	// VP8L payload: 1 byte signature 0x2F + 4 bytes (width/height/has-alpha/version).
	// The contents don't need to be valid bitstream — just enough that
	// the inspector recognizes the chunk type.
	vp8l := []byte{0x2F, 0x00, 0x00, 0x00, 0x00}
	// RIFF header + WEBP + VP8L chunk header.
	chunkLen := len(vp8l)
	// Padding to even byte count (RIFF requires even chunk size on disk;
	// length field counts unpadded data).
	if chunkLen%2 == 1 {
		chunkLen++
	}
	totalSize := 4 + 4 + 4 + chunkLen // "WEBP" + chunkID + chunkLen + payload
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(totalSize))
	b.WriteString("WEBP")
	b.WriteString("VP8L")
	binary.Write(&b, binary.LittleEndian, uint32(len(vp8l)))
	b.Write(vp8l)
	if len(vp8l)%2 == 1 {
		b.WriteByte(0)
	}
	return b.Bytes()
}

// buildWebPVP8 builds a minimal valid-looking RIFF/WEBP container
// with a VP8 (lossy) chunk. The Y AC quantizer is at a known offset
// in the VP8 frame header.
func buildWebPVP8(yACQuantIdx byte) []byte {
	// VP8 frame header layout (intra-only key frame): the Y AC
	// quantizer index is part of the second-pass header after the
	// uncompressed start code (3 bytes 0x9D 0x01 0x2A).
	// For testing, we just need the inspector to find the VP8 chunk
	// and read the QP byte at the expected offset.
	//
	// Minimum bytes the inspector reads: enough to locate the QP. Per
	// the inspector implementation, look at the value at offset 14
	// from the start of the VP8 chunk payload (frame_tag bits + scale +
	// segment_header + filter_header + token_partitions + quantizer).
	// We pad zeros and place yACQuantIdx at the QP offset.
	const minVP8 = 32
	vp8 := make([]byte, minVP8)
	// 3-byte uncompressed start-code marker at offset 3.
	vp8[3] = 0x9D
	vp8[4] = 0x01
	vp8[5] = 0x2A
	// QP at the inspector's read offset (see webp.go's qpOffset).
	vp8[qpOffsetForTest] = yACQuantIdx

	var b bytes.Buffer
	chunkLen := len(vp8)
	if chunkLen%2 == 1 {
		chunkLen++
	}
	totalSize := 4 + 4 + 4 + chunkLen
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(totalSize))
	b.WriteString("WEBP")
	b.WriteString("VP8 ")
	binary.Write(&b, binary.LittleEndian, uint32(len(vp8)))
	b.Write(vp8)
	if len(vp8)%2 == 1 {
		b.WriteByte(0)
	}
	return b.Bytes()
}

func TestWebPInspectorRegistered(t *testing.T) {
	insp, ok := quality.For(opentile.CompressionWebP)
	if !ok {
		t.Fatalf("WebP inspector not registered")
	}
	if insp.Compression() != opentile.CompressionWebP {
		t.Errorf("Compression(): got %v, want WebP", insp.Compression())
	}
}

func TestInspectWebPLossless(t *testing.T) {
	insp := &inspector{}
	info, err := insp.Inspect(buildWebPVP8L())
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !info.Lossless {
		t.Error("Lossless: got false, want true (VP8L)")
	}
	if info.Codec != "WebP" {
		t.Errorf("Codec: got %q", info.Codec)
	}
}

func TestInspectWebPLossy(t *testing.T) {
	insp := &inspector{}
	info, err := insp.Inspect(buildWebPVP8(64)) // mid-QP
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.Lossless {
		t.Error("Lossless: got true, want false (VP8)")
	}
	if info.Codec != "WebP" {
		t.Errorf("Codec: got %q", info.Codec)
	}
	if info.QualityEstimate < 1 || info.QualityEstimate > 99 {
		t.Errorf("QualityEstimate: %d should be a meaningful value", info.QualityEstimate)
	}
}

func TestInspectWebPNotAWebP(t *testing.T) {
	insp := &inspector{}
	_, err := insp.Inspect([]byte("Not RIFF/WEBP"))
	if err == nil {
		t.Error("expected error on non-WebP bytes")
	}
}
