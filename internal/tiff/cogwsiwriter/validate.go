package cogwsiwriter

import (
	"errors"
	"fmt"

	"github.com/wsilabs/wsitools/internal/tiff"
)

// ErrInvalidAssocType is returned by AddAssociated when the spec's
// Type isn't one of the four COG-WSI v0.1 §6 allowed values.
var ErrInvalidAssocType = errors.New("invalid associated image type")

// validAssocTypes is the COG-WSI v0.1 set of allowed WSIImageType
// values for associated-image IFDs. Stricter than the general
// tiff.ValidateWSIImageType set (which permits probability, map,
// associated as well).
var validAssocTypes = map[string]bool{
	tiff.WSIImageTypeLabel:     true,
	tiff.WSIImageTypeMacro:     true,
	tiff.WSIImageTypeThumbnail: true,
	tiff.WSIImageTypeOverview:  true,
}

// validateAssocType returns nil if typ is one of the four allowed
// associated-image types; otherwise wraps ErrInvalidAssocType.
func validateAssocType(typ string) error {
	if !validAssocTypes[typ] {
		return fmt.Errorf("cogwsi: invalid associated type %q (want one of label|macro|thumbnail|overview): %w", typ, ErrInvalidAssocType)
	}
	return nil
}
