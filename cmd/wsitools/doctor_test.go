package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestDoctorReportsMemory runs the built binary's `doctor` and checks the
// Memory section is present with a Soft limit line.
func TestDoctorReportsMemory(t *testing.T) {
	bin := stripedBinary(t) // reuse helper from striped_formats_test.go
	out, err := exec.Command(bin, "doctor").CombinedOutput()
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, out)
	}
	text := string(out)
	if !strings.Contains(text, "Memory:") {
		t.Errorf("doctor output missing 'Memory:' section:\n%s", text)
	}
	if !strings.Contains(text, "Soft limit:") {
		t.Errorf("doctor output missing 'Soft limit:' line:\n%s", text)
	}
}
