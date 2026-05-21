package tiff

// BigTIFFMode controls classic-vs-BigTIFF selection in writers.
type BigTIFFMode int

const (
	BigTIFFAuto BigTIFFMode = iota
	BigTIFFOn
	BigTIFFOff
)

// safetyMargin is the byte budget added on top of caller-supplied
// dataBytes + metaBytes to leave room for write-time padding and tag-
// array growth before crossing the 2 GiB threshold.
const safetyMargin = 64 * 1024

// AutoPromote reports whether predicted output > 2 GiB.
// Total = dataBytes + metaBytes + safetyMargin; promote when total > 2 GiB.
// The 2 GiB threshold (rather than the classic-TIFF 4 GiB ceiling)
// leaves ample headroom for late-discovered metadata.
func AutoPromote(dataBytes, metaBytes uint64) bool {
	return dataBytes+metaBytes+safetyMargin > (2 << 30)
}

// Resolve applies a BigTIFFMode against a predicted byte total.
// BigTIFFOn returns true; BigTIFFOff returns false; BigTIFFAuto
// returns AutoPromote(predictedBytes, 0).
func Resolve(mode BigTIFFMode, predictedBytes uint64) bool {
	switch mode {
	case BigTIFFOn:
		return true
	case BigTIFFOff:
		return false
	}
	return AutoPromote(predictedBytes, 0)
}
