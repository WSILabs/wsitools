package main

import (
	"testing"

	opentile "github.com/wsilabs/opentile-go"
)

// l0WidthAndCodec opens path with opentile and returns the L0 pixel width and
// compression — a small shared helper so binary-shelling tests can assert the
// ACTUAL output (a transform applied / codec honored), not just exit 0.
func l0WidthAndCodec(t *testing.T, path string) (int, opentile.Compression) {
	t.Helper()
	s, err := opentile.OpenFile(path)
	if err != nil {
		t.Fatalf("opentile.OpenFile(%s): %v", path, err)
	}
	defer s.Close()
	lv := s.Levels()
	if len(lv) == 0 {
		t.Fatalf("no levels in %s", path)
	}
	return lv[0].Size.W, lv[0].Compression
}
