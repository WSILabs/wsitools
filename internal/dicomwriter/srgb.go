package dicomwriter

import _ "embed"

// srgbICCProfile is a canonical sRGB (IEC 61966-2.1) ICC profile, embedded so a
// WSM instance can satisfy the Type 1C ICCProfile requirement when the source
// carries no embedded profile (e.g. many SVS files). Built with Little-CMS
// (via the build step in the Phase 1 plan) — a generated, freely-redistributable
// profile, not a vendor asset.
//
//go:embed srgb.icc
var srgbICCProfile []byte
