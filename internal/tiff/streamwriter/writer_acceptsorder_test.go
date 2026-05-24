package streamwriter

import (
	"testing"

	"github.com/wsilabs/wsitools/internal/tiff/tileorder"
)

func TestAcceptsOrderPermissive(t *testing.T) {
	w := &Writer{} // no acceptedOrders set
	if !w.AcceptsOrder(tileorder.RowMajor) {
		t.Errorf("permissive writer should accept RowMajor")
	}
	if !w.AcceptsOrder(tileorder.HilbertCurve) {
		t.Errorf("permissive writer should accept HilbertCurve")
	}
}

func TestAcceptsOrderRestrictive(t *testing.T) {
	w := &Writer{acceptedOrders: map[string]bool{"row-major": true}}
	if !w.AcceptsOrder(tileorder.RowMajor) {
		t.Errorf("row-major-only writer should accept RowMajor")
	}
	if w.AcceptsOrder(tileorder.HilbertCurve) {
		t.Errorf("row-major-only writer should reject HilbertCurve")
	}
}
