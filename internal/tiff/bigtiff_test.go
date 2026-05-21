package tiff

import "testing"

func TestBigTIFFModeResolveAuto(t *testing.T) {
	if Resolve(BigTIFFAuto, 100*1024*1024) {
		t.Errorf("100 MiB should not promote")
	}
	if !Resolve(BigTIFFAuto, 3*(1<<30)) {
		t.Errorf("3 GiB should promote")
	}
}

func TestBigTIFFModeResolveOverrides(t *testing.T) {
	if !Resolve(BigTIFFOn, 100) {
		t.Errorf("BigTIFFOn must promote regardless of size")
	}
	if Resolve(BigTIFFOff, 100*(1<<30)) {
		t.Errorf("BigTIFFOff must NOT promote regardless of size")
	}
}

func TestAutoPromoteThreshold(t *testing.T) {
	if AutoPromote(2*(1<<30)-65536, 0) {
		t.Errorf("2 GiB - 64 KiB should not promote")
	}
	if !AutoPromote(2*(1<<30)+1, 0) {
		t.Errorf("2 GiB + 1 should promote")
	}
	if !AutoPromote(2*(1<<30)-100, 200) {
		t.Errorf("data+meta over 2 GiB should promote")
	}
}
