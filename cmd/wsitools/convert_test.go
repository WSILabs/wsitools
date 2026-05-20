package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestConvertHelpListsRequiredFlags(t *testing.T) {
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"convert", "--help"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"--to", "--output", "--force", "cog-wsi"} {
		if !strings.Contains(out, want) {
			t.Errorf("help output missing %q\n%s", want, out)
		}
	}
}
