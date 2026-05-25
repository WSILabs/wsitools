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
