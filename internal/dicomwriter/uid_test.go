package dicomwriter

import (
	"regexp"
	"testing"
)

func TestNewUID(t *testing.T) {
	re := regexp.MustCompile(`^2\.25\.[0-9]+$`)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		u := NewUID()
		if !re.MatchString(u) {
			t.Fatalf("UID %q not 2.25.<int>", u)
		}
		if len(u) > 64 {
			t.Fatalf("UID %q exceeds 64 chars (DICOM UI limit)", u)
		}
		if seen[u] {
			t.Fatalf("duplicate UID %q", u)
		}
		seen[u] = true
	}
}
