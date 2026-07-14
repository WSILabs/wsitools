package quality

import (
	"errors"
	"sync"

	opentile "github.com/wsilabs/opentile-go"
)

// Info is the structured quality summary for a single tile. Fields
// are sparsely populated — codecs surface only what's meaningful.
type Info struct {
	// Codec is a human-readable name: "JPEG", "JPEG 2000", "WebP",
	// "lossless" (for LZW/Deflate/None), "unknown" (no inspector
	// registered). Used in info's text output.
	Codec string `json:"codec"`

	// Lossless indicates whether the codec preserves source pixels
	// exactly. true for LZW/Deflate/None and lossless modes of
	// WebP / JPEG XL. false for JPEG, JPEG 2000 (irreversible),
	// lossy WebP, etc.
	Lossless bool `json:"lossless"`

	// QualityEstimate is a normalized 0-100 quality score. 0 if
	// not applicable (lossless codecs) or unknown.
	QualityEstimate int `json:"quality_estimate,omitempty"`

	// ChromaSubsampling is "4:4:4" / "4:2:2" / "4:2:0" / "4:1:1" or
	// empty if not applicable.
	ChromaSubsampling string `json:"chroma_subsampling,omitempty"`

	// Colorspace is the EFFECTIVE (decoded) colorspace of the tile:
	// "RGB", "YCbCr", or "grayscale". Empty when it can't be
	// determined (no codestream-inspector for the codec, or an
	// ambiguous codestream with no colorspace box). A JPEG 2000 tile
	// carrying an MCT (ICT/RCT) decorrelating transform reports "RGB"
	// — the transform is inverted on decode — matching what a reader
	// actually sees. (mirrors validate's #44 colorspace check)
	Colorspace string `json:"colorspace,omitempty"`

	// BitDepth is the bits per component of the tile codestream (8 for
	// brightfield; 16 for some fluorescence / JPEG 2000). 0 when the
	// codec exposes no header-only bit-depth signal.
	BitDepth int `json:"bit_depth,omitempty"`

	// LayerCount is the number of progressive layers (JPEG 2000) or
	// 0 if not applicable.
	LayerCount int `json:"layer_count,omitempty"`

	// Notes is freeform codec-specific commentary surfaced in the
	// text output.
	Notes string `json:"notes,omitempty"`
}

// Inspector extracts Info from a single compressed tile.
// Implementations are pure functions of the tile bytes.
type Inspector interface {
	// Compression returns the codec this inspector handles.
	Compression() opentile.Compression

	// Inspect parses tileBytes and returns the codec's Info. May
	// return ErrCorruptOrMismatch if the bytes don't match the
	// declared codec.
	Inspect(tileBytes []byte) (Info, error)
}

// ErrCorruptOrMismatch is returned by Inspect when the bytes don't
// match the expected codec signature.
var ErrCorruptOrMismatch = errors.New("quality: tile bytes don't match expected codec")

var (
	regMu sync.RWMutex
	reg   = map[opentile.Compression]Inspector{}
)

// Register adds an inspector to the global registry. Called from
// each codec subpackage's init(). Last-in-wins on conflict.
func Register(i Inspector) {
	regMu.Lock()
	defer regMu.Unlock()
	reg[i.Compression()] = i
}

// For returns the registered inspector for the given codec, or
// (nil, false) if none.
func For(c opentile.Compression) (Inspector, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	i, ok := reg[c]
	return i, ok
}

// Registered returns the canonical compressions of every registered
// inspector. Order unspecified.
func Registered() []opentile.Compression {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]opentile.Compression, 0, len(reg))
	for c := range reg {
		out = append(out, c)
	}
	return out
}
