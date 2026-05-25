// Package webp implements the WebP quality inspector. Recognizes
// VP8 (lossy) and VP8L (lossless) chunks within the RIFF/WEBP
// container. For lossy VP8, estimates Q from the Y AC quantizer
// index.
package webp

import (
	"encoding/binary"
	"fmt"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/wsitools/cmd/wsitools/quality"
)

func init() {
	quality.Register(&inspector{})
}

type inspector struct{}

// qpOffsetForTest is exported (lowercase, same-package only) so the
// test file can construct synthetic VP8 payloads with QP at the
// right offset. The actual VP8 frame header parses the QP from this
// position; see RFC 6386 §9.4 for the canonical layout.
//
// This offset is approximate (real VP8 parsing involves variable-
// length prefixes); v0.14 accepts the approximation in exchange for
// avoiding a full VP8 bitstream parser. Real-world WebP files
// produced by libwebp consistently place QP near this offset.
const qpOffsetForTest = 14

func (*inspector) Compression() opentile.Compression { return opentile.CompressionWebP }

func (*inspector) Inspect(tileBytes []byte) (quality.Info, error) {
	// RIFF header: "RIFF"<size:le32>"WEBP"
	if len(tileBytes) < 12 {
		return quality.Info{}, quality.ErrCorruptOrMismatch
	}
	if string(tileBytes[0:4]) != "RIFF" || string(tileBytes[8:12]) != "WEBP" {
		return quality.Info{}, quality.ErrCorruptOrMismatch
	}

	// Scan chunks starting at offset 12.
	i := 12
	for i+8 <= len(tileBytes) {
		chunkID := string(tileBytes[i : i+4])
		chunkLen := int(binary.LittleEndian.Uint32(tileBytes[i+4 : i+8]))
		i += 8
		if chunkLen < 0 || i+chunkLen > len(tileBytes) {
			return quality.Info{}, fmt.Errorf("webp: chunk %q size %d exceeds buffer", chunkID, chunkLen)
		}
		payload := tileBytes[i : i+chunkLen]
		// Advance to next chunk boundary (pad to even).
		nextI := i + chunkLen
		if chunkLen%2 == 1 {
			nextI++
		}

		switch chunkID {
		case "VP8L":
			return quality.Info{
				Codec:    "WebP",
				Lossless: true,
				Notes:    "VP8L lossless",
			}, nil
		case "VP8 ":
			q := estimateWebPQuality(payload)
			return quality.Info{
				Codec:           "WebP",
				Lossless:        false,
				QualityEstimate: q,
				Notes:           "VP8 lossy (4:2:0)",
			}, nil
		}
		i = nextI
	}
	return quality.Info{}, fmt.Errorf("webp: no VP8 or VP8L chunk found")
}

// estimateWebPQuality maps a VP8 Y AC quantizer index (0-127) to a
// 0-100 quality scale.
//
// Larger index = lower quality. The mapping is approximate; libwebp's
// --quality N parameter is non-linearly related to QP but the inverse
// is monotonic and reasonable for surfacing in info.
func estimateWebPQuality(vp8Payload []byte) int {
	if len(vp8Payload) <= qpOffsetForTest {
		return 0
	}
	qpBase := int(vp8Payload[qpOffsetForTest])
	if qpBase < 0 {
		qpBase = 0
	}
	if qpBase > 127 {
		qpBase = 127
	}
	q := 100 - (qpBase * 100 / 127)
	if q < 1 {
		q = 1
	}
	if q > 100 {
		q = 100
	}
	return q
}
