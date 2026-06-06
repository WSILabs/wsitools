package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff/edit"
)

func TestLocateAssociatedSVSLabel(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../sample_files"
	}
	var p string
	for _, c := range []string{
		filepath.Join(dir, "svs", "CMU-1-Small-Region.svs"),
		filepath.Join(dir, "svs", "CMU-1.svs"),
	} {
		if _, err := os.Stat(c); err == nil {
			p = c
			break
		}
	}
	if p == "" {
		t.Skip("no SVS fixture present")
	}
	src, err := source.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	hasLabel := false
	for _, a := range src.Associated() {
		if a.Type() == "label" {
			hasLabel = true
		}
	}
	if !hasLabel {
		t.Skip("fixture has no label")
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	f, err := edit.Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	idx, a, err := locateAssociated(src, f, "label")
	if err != nil {
		t.Fatalf("locate: %v", err)
	}
	if idx < 0 || idx >= len(f.IFDs) {
		t.Fatalf("idx %d out of range (have %d IFDs)", idx, len(f.IFDs))
	}
	if a.Type() != "label" {
		t.Fatalf("type = %q", a.Type())
	}
}

func TestLocateAssociatedMissing(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../sample_files"
	}
	var p string
	for _, c := range []string{
		filepath.Join(dir, "svs", "CMU-1-Small-Region.svs"),
		filepath.Join(dir, "svs", "CMU-1.svs"),
	} {
		if _, err := os.Stat(c); err == nil {
			p = c
			break
		}
	}
	if p == "" {
		t.Skip("no SVS fixture present")
	}
	src, _ := source.Open(p)
	defer src.Close()
	data, _ := os.ReadFile(p)
	f, _ := edit.Parse(bytes.NewReader(data), int64(len(data)))
	if _, _, err := locateAssociated(src, f, "no-such-type"); err == nil {
		t.Fatal("expected error for absent type")
	}
}
