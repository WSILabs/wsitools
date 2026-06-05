package cogwsiwriter

import (
	"errors"
	"testing"
)

func TestValidateAssocTypeAcceptsAllowed(t *testing.T) {
	for _, ty := range []string{"label", "macro", "thumbnail", "overview"} {
		if err := validateAssocType(ty); err != nil {
			t.Errorf("validateAssocType(%q): unexpected error %v", ty, err)
		}
	}
}

func TestValidateAssocTypeRejectsOther(t *testing.T) {
	for _, ty := range []string{"", "pyramid", "probability", "map", "associated"} {
		err := validateAssocType(ty)
		if err == nil {
			t.Errorf("validateAssocType(%q): expected error", ty)
			continue
		}
		if !errors.Is(err, ErrInvalidAssocType) {
			t.Errorf("validateAssocType(%q): error should wrap ErrInvalidAssocType, got %v", ty, err)
		}
	}
}
