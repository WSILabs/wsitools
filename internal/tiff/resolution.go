package tiff

import "math"

// MPPToResolution converts microns-per-pixel to a TIFF XResolution /
// YResolution RATIONAL expressed in pixels-per-centimeter (pair with
// ResolutionUnit = ResolutionUnitCentimeter). Returns (0, 0) when mpp
// is not a usable positive value.
//
// pixels/cm = 10000 µm/cm ÷ mpp µm/px. The numerator is scaled by a
// denominator chosen to keep it within uint32 across the realistic MPP
// range; for extreme (tiny) MPP it falls back to denom=1.
func MPPToResolution(mpp float64) (num, denom uint32) {
	if mpp <= 0 || math.IsNaN(mpp) || math.IsInf(mpp, 0) {
		return 0, 0
	}
	pxPerCm := 10000.0 / mpp
	if scaled := pxPerCm * 10.0; scaled <= float64(math.MaxUint32) {
		return uint32(math.Round(scaled)), 10
	}
	if pxPerCm <= float64(math.MaxUint32) {
		return uint32(math.Round(pxPerCm)), 1
	}
	return math.MaxUint32, 1 // saturate; unreachable for real slides
}
