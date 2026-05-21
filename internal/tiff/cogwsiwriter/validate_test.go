package cogwsiwriter

import (
	"errors"
	"testing"
)

func TestValidateAssocKindAcceptsAllowed(t *testing.T) {
	for _, k := range []string{"label", "macro", "thumbnail", "overview"} {
		if err := validateAssocKind(k); err != nil {
			t.Errorf("validateAssocKind(%q): unexpected error %v", k, err)
		}
	}
}

func TestValidateAssocKindRejectsOther(t *testing.T) {
	for _, k := range []string{"", "pyramid", "probability", "map", "associated"} {
		err := validateAssocKind(k)
		if err == nil {
			t.Errorf("validateAssocKind(%q): expected error", k)
			continue
		}
		if !errors.Is(err, ErrInvalidAssocKind) {
			t.Errorf("validateAssocKind(%q): error should wrap ErrInvalidAssocKind, got %v", k, err)
		}
	}
}
