package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestConvertFailsForMissingInput(t *testing.T) {
	dir := t.TempDir()
	rootCmd.SetArgs([]string{"convert", "--to", "cog-wsi", "-o", dir + "/out.tiff", dir + "/missing.svs"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("expected error for missing input")
	}
}

func TestConvertFailsForBadTo(t *testing.T) {
	dir := t.TempDir()
	tmp, _ := os.Create(dir + "/in.tiff")
	tmp.Close()
	rootCmd.SetArgs([]string{"convert", "--to", "iris", "-o", dir + "/out.tiff", dir + "/in.tiff"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	err := rootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "only 'cog-wsi'") {
		t.Errorf("expected unsupported --to error, got %v", err)
	}
}

func TestConvertFailsWhenOutputExists(t *testing.T) {
	dir := t.TempDir()
	in, _ := os.Create(dir + "/in.tiff")
	in.Close()
	out, _ := os.Create(dir + "/out.tiff")
	out.Close()
	rootCmd.SetArgs([]string{"convert", "--to", "cog-wsi", "-o", dir + "/out.tiff", dir + "/in.tiff"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	err := rootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected 'already exists' error, got %v", err)
	}
}

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
