package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runDumpIFDsCmd(t *testing.T, args ...string) string {
	t.Helper()
	dumpIFDsRaw = false
	dumpIFDsRawFull = false
	*dumpIFDsJSON = false

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs(append([]string{"dump-ifds"}, args...))
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("dump-ifds %v: %v\noutput:\n%s", args, err, buf.String())
	}
	return buf.String()
}

func TestDumpIFDsRawCMU1Small(t *testing.T) {
	dir := testDir(t)
	path := filepath.Join(dir, "svs/CMU-1-Small-Region.svs")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	out := runDumpIFDsCmd(t, "--raw", path)
	for _, want := range []string{
		"ImageWidth", "Compression", "ImageDescription",
		"TileWidth", "JPEGTables",
		"JPEG (7)",
		"RGB (2)",
		"chunky (1)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestDumpIFDsRawJSON(t *testing.T) {
	dir := testDir(t)
	path := filepath.Join(dir, "svs/CMU-1-Small-Region.svs")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	out := runDumpIFDsCmd(t, "--raw", "--json", path)
	var ifds []map[string]any
	if err := json.Unmarshal([]byte(out), &ifds); err != nil {
		t.Fatalf("invalid JSON: %v\noutput:\n%s", err, out)
	}
	if len(ifds) < 1 {
		t.Fatalf("got %d IFDs, want >=1", len(ifds))
	}
	entries := ifds[0]["entries"].([]any)
	var found map[string]any
	for _, e := range entries {
		em := e.(map[string]any)
		if int(em["tag"].(float64)) == 256 {
			found = em
			break
		}
	}
	if found == nil {
		t.Fatal("tag 256 not found in IFD 0 entries")
	}
	if found["name"] != "ImageWidth" {
		t.Errorf("name = %v, want ImageWidth", found["name"])
	}
}

func TestDumpIFDsRawNDPI(t *testing.T) {
	dir := testDir(t)
	path := filepath.Join(dir, "ndpi/CMU-1.ndpi")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	out := runDumpIFDsCmd(t, "--raw", path)
	if !strings.Contains(out, "(unknown)") {
		t.Errorf("expected at least one unknown private tag in NDPI output")
	}
}

func TestDumpIFDsRawTruncation(t *testing.T) {
	dir := testDir(t)
	path := filepath.Join(dir, "svs/CMU-1-Small-Region.svs")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	defaultOut := runDumpIFDsCmd(t, "--raw", path)
	fullOut := runDumpIFDsCmd(t, "--raw", "--raw-full", path)
	if !strings.Contains(defaultOut, "(... ") {
		t.Errorf("expected truncation marker '(... N more)' in default --raw output")
	}
	if strings.Contains(fullOut, "(... ") {
		t.Errorf("expected no truncation marker under --raw-full")
	}
}

func TestDumpIFDsRejectsDICOM(t *testing.T) {
	bin := strippedBinary(t)
	dir := filepath.Join(testDir(t), "dicom", "Leica-4")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("no DICOM fixture at %s", dir)
	}
	out, err := runBin(bin, "dump-ifds", dir)
	if err == nil {
		t.Fatalf("expected error, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "TIFF-dialect") {
		t.Errorf("error should explain DICOM is not a TIFF-dialect source, got:\n%s", out)
	}
}
