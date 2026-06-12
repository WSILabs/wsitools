package dicomwriter

import "testing"

func TestSRGBProfileValid(t *testing.T) {
	if len(srgbICCProfile) < 128 {
		t.Fatalf("srgbICCProfile too small: %d bytes", len(srgbICCProfile))
	}
	// ICC profiles carry the 'acsp' signature at byte offset 36.
	if got := string(srgbICCProfile[36:40]); got != "acsp" {
		t.Errorf("ICC signature = %q, want \"acsp\"", got)
	}
}
