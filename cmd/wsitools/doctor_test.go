package main

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// TestRenderCodecHealth verifies doctor distinguishes a codec that can encode
// (✓) from one whose library linked but cannot encode (✗ + reason) — the
// "library present, encoder unavailable" case (e.g. libavif with no AV1 encoder
// on Windows, wsitools#34).
func TestRenderCodecHealth(t *testing.T) {
	var buf bytes.Buffer
	renderCodecHealth(&buf, []codecStatus{
		{Name: "jpeg", OK: true},
		{Name: "avif", OK: false, Detail: "codec/avif: encode failed: No codec available"},
	})
	out := buf.String()
	if !strings.Contains(out, "✓ jpeg") {
		t.Errorf("missing working-codec line:\n%s", out)
	}
	if !strings.Contains(out, "✗ avif") ||
		!strings.Contains(out, "library present, encoder unavailable") ||
		!strings.Contains(out, "No codec available") {
		t.Errorf("missing 'library present, encoder unavailable' line with reason:\n%s", out)
	}
}

// TestProbeCodecsAllEncode confirms every registered codec can actually encode on
// a fully-built binary (the probe itself is sound — a false ✗ would be a probe
// bug, not a codec gap). On a build missing a backend this test would correctly
// fail, flagging the packaging gap.
func TestProbeCodecsAllEncode(t *testing.T) {
	for _, st := range probeCodecs() {
		if !st.OK {
			t.Errorf("codec %s failed encode probe: %s", st.Name, st.Detail)
		}
	}
}

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
