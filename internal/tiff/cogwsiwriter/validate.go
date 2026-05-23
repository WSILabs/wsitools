package cogwsiwriter

import (
	"errors"
	"fmt"

	"github.com/wsilabs/wsitools/internal/tiff"
)

// ErrInvalidAssocKind is returned by AddAssociated when the spec's
// Kind isn't one of the four COG-WSI v0.1 §6 allowed values.
var ErrInvalidAssocKind = errors.New("invalid associated image kind")

// validAssocKinds is the COG-WSI v0.1 set of allowed WSIImageType
// values for associated-image IFDs. Stricter than the general
// tiff.ValidateWSIImageType set (which permits probability, map,
// associated as well).
var validAssocKinds = map[string]bool{
	tiff.WSIImageTypeLabel:     true,
	tiff.WSIImageTypeMacro:     true,
	tiff.WSIImageTypeThumbnail: true,
	tiff.WSIImageTypeOverview:  true,
}

// validateAssocKind returns nil if kind is one of the four allowed
// associated-image kinds; otherwise wraps ErrInvalidAssocKind.
func validateAssocKind(kind string) error {
	if !validAssocKinds[kind] {
		return fmt.Errorf("cogwsi: invalid associated kind %q (want one of label|macro|thumbnail|overview): %w", kind, ErrInvalidAssocKind)
	}
	return nil
}
