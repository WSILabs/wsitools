package main

import (
	"strings"
	"testing"
)

func TestGuardRoutesFourTargetsErrorsDicomBif(t *testing.T) {
	for _, tgt := range []string{"cog-wsi", "svs", "tiff", "ome-tiff", "dzi", "szi", ""} {
		if !guardTargetHandlesOverlap(tgt) {
			t.Errorf("target %q should be overlap-capable (route, not error)", tgt)
		}
	}
	for _, tgt := range []string{"dicom", "bif"} {
		if guardTargetHandlesOverlap(tgt) {
			t.Errorf("target %q should NOT be overlap-capable (still guarded)", tgt)
		}
	}
	err := overlapGuardError("x.bif", "dicom")
	if err == nil || !strings.Contains(err.Error(), "dicom") {
		t.Errorf("guarded-target error = %v, want mention of dicom", err)
	}
}
