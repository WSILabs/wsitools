package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
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
	if err == nil || !strings.Contains(err.Error(), "unknown target") {
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

func TestConvertToSVSReencode(t *testing.T) {
	bin := findBinary(t)
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), "GitHub/opentile-go/sample_files")
	}
	in := filepath.Join(dir, "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(in); err != nil {
		t.Skip("fixture missing: " + in)
	}
	out := filepath.Join(t.TempDir(), "out.svs")
	cmd := exec.Command(bin,
		"convert", "--to", "svs", "--codec", "jpeg", "--quality", "85", "-o", out, in)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("convert: %v\n%s", err, b)
	}
	fi, err := os.Stat(out)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("output missing or empty")
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
