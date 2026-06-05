package tiff

import "fmt"

// WSIImageType canonical values. Lowercase to match opentile-go's
// AssociatedImage.Type() vocabulary.
const (
	WSIImageTypePyramid     = "pyramid"
	WSIImageTypeLabel       = "label"
	WSIImageTypeMacro       = "macro"
	WSIImageTypeOverview    = "overview"
	WSIImageTypeThumbnail   = "thumbnail"
	WSIImageTypeProbability = "probability"
	WSIImageTypeMap         = "map"
	WSIImageTypeAssociated  = "associated"
)

var validWSIImageTypes = map[string]bool{
	WSIImageTypePyramid:     true,
	WSIImageTypeLabel:       true,
	WSIImageTypeMacro:       true,
	WSIImageTypeOverview:    true,
	WSIImageTypeThumbnail:   true,
	WSIImageTypeProbability: true,
	WSIImageTypeMap:         true,
	WSIImageTypeAssociated:  true,
}

// ValidateWSIImageType returns nil if v is one of the canonical
// WSIImageType values; otherwise returns a descriptive error.
// Stricter subsets (e.g. cogwsi's 4-value associated-image set) live
// in the writer packages that enforce them.
func ValidateWSIImageType(v string) error {
	if !validWSIImageTypes[v] {
		return fmt.Errorf("tiff: invalid WSIImageType %q (want one of pyramid|label|macro|overview|thumbnail|probability|map|associated)", v)
	}
	return nil
}
