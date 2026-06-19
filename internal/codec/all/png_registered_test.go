package all

import (
	"testing"

	"github.com/wsilabs/wsitools/internal/codec"
)

// TestPNGRegisteredViaAll confirms importing the aggregator registers png.
func TestPNGRegisteredViaAll(t *testing.T) {
	if _, err := codec.Lookup("png"); err != nil {
		t.Fatalf("png not registered via internal/codec/all: %v", err)
	}
}
