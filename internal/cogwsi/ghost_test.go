package cogwsi

import (
	"fmt"
	"strings"
	"testing"
)

func TestGhostMarshalRoundTrip(t *testing.T) {
	g := defaultGhost()
	b, err := g.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.HasPrefix(string(b), "GDAL_STRUCTURAL_METADATA_SIZE=") {
		t.Errorf("missing size header: %q", string(b))
	}
	for _, want := range []string{
		"LAYOUT=IFDS_BEFORE_DATA",
		"BLOCK_ORDER=ROW_MAJOR",
		"BLOCK_LEADER=SIZE_AS_UINT4",
		"BLOCK_TRAILER=LAST_4_BYTES_REPEATED",
		"KNOWN_INCOMPATIBLE_EDITION=NO",
		"COG_WSI_VERSION=0.1",
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing key %q in: %s", want, string(b))
		}
	}
	parsed, err := ParseGhost(b)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.Version != "0.1" {
		t.Errorf("COG_WSI_VERSION: got %q want 0.1", parsed.Version)
	}
}

func TestGhostMarshalSizeHeaderIsAccurate(t *testing.T) {
	g := defaultGhost()
	b, err := g.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	// Find the first newline; everything before it is the size line.
	nl := strings.IndexByte(string(b), '\n')
	if nl < 0 {
		t.Fatalf("no newline in ghost area")
	}
	sizeLine := string(b[:nl])
	want := len(b) - nl - 1 // remaining bytes after size line + newline
	// Format: GDAL_STRUCTURAL_METADATA_SIZE=NNNNNN bytes
	var n int
	if _, err := fmt.Sscanf(sizeLine, "GDAL_STRUCTURAL_METADATA_SIZE=%d bytes", &n); err != nil {
		t.Fatalf("parse size line %q: %v", sizeLine, err)
	}
	if n != want {
		t.Errorf("declared size %d, actual remainder %d", n, want)
	}
}
