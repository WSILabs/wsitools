package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
)

func TestReportFails(t *testing.T) {
	mk := func(sev opentile.Severity) *opentile.Report {
		return &opentile.Report{Findings: []opentile.Finding{
			{Severity: sev, Code: "x", Message: "m", Pyramid: -1, Level: -1, Count: 1},
		}}
	}
	cases := []struct {
		name   string
		report *opentile.Report
		strict bool
		want   bool
	}{
		{"clean", &opentile.Report{}, false, false},
		{"clean-strict", &opentile.Report{}, true, false},
		{"info-only", mk(opentile.Info), false, false},
		{"info-only-strict", mk(opentile.Info), true, false},
		{"warning-lenient", mk(opentile.Warning), false, false},
		{"warning-strict", mk(opentile.Warning), true, true},
		{"error-lenient", mk(opentile.Error), false, true},
		{"error-strict", mk(opentile.Error), true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := reportFails(c.report, c.strict); got != c.want {
				t.Errorf("reportFails(%s, strict=%v) = %v, want %v", c.name, c.strict, got, c.want)
			}
		})
	}
}

func TestLocusPtr(t *testing.T) {
	if got := locusPtr(-1); got != nil {
		t.Errorf("locusPtr(-1) = %v, want nil", got)
	}
	if got := locusPtr(0); got == nil || *got != 0 {
		t.Errorf("locusPtr(0) = %v, want *0", got)
	}
	if got := locusPtr(3); got == nil || *got != 3 {
		t.Errorf("locusPtr(3) = %v, want *3", got)
	}
}

func TestFormatName(t *testing.T) {
	if got := formatName(opentile.FormatUnknown); got != "unknown" {
		t.Errorf("formatName(unknown) = %q, want %q", got, "unknown")
	}
	if got := formatName(opentile.Format("svs")); got != "svs" {
		t.Errorf("formatName(svs) = %q, want %q", got, "svs")
	}
}

func TestFormatLocus(t *testing.T) {
	p0, l3 := 0, 3
	cases := []struct {
		name           string
		pyramid, level *int
		count          int
		want           string
	}{
		{"both+count", &p0, &l3, 200, "P0/L3 ×200"},
		{"both", &p0, &l3, 1, "P0/L3"},
		{"pyramid-only", &p0, nil, 1, "P0"},
		{"pyramid-only+count", &p0, nil, 5, "P0 ×5"},
		{"whole-file", nil, nil, 1, ""},
		{"whole-file+count", nil, nil, 4, "×4"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := formatLocus(c.pyramid, c.level, c.count); got != c.want {
				t.Errorf("formatLocus = %q, want %q", got, c.want)
			}
		})
	}
}

func TestBuildValidateResult(t *testing.T) {
	report := &opentile.Report{
		Format: opentile.Format("svs"),
		Findings: []opentile.Finding{
			{Severity: opentile.Error, Code: "tile-grid-mismatch", Message: "grid", Pyramid: 0, Level: 3, Count: 200},
			{Severity: opentile.Warning, Code: "missing-metadata", Message: "no mpp", Pyramid: -1, Level: -1, Count: 1},
		},
	}
	res := buildValidateResult("a.svs", report)

	if res.Path != "a.svs" || res.Format != "svs" {
		t.Errorf("path/format = %q/%q", res.Path, res.Format)
	}
	if res.OK {
		t.Errorf("OK = true, want false")
	}
	if res.Worst != "error" {
		t.Errorf("Worst = %q, want error", res.Worst)
	}
	if len(res.Findings) != 2 {
		t.Fatalf("findings = %d, want 2", len(res.Findings))
	}
	f0 := res.Findings[0]
	if f0.Severity != "error" || f0.Code != "tile-grid-mismatch" || f0.Count != 200 {
		t.Errorf("finding[0] = %+v", f0)
	}
	if f0.Pyramid == nil || *f0.Pyramid != 0 || f0.Level == nil || *f0.Level != 3 {
		t.Errorf("finding[0] locus = %v/%v, want 0/3", f0.Pyramid, f0.Level)
	}
	f1 := res.Findings[1]
	if f1.Pyramid != nil || f1.Level != nil {
		t.Errorf("finding[1] locus = %v/%v, want nil/nil", f1.Pyramid, f1.Level)
	}

	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var round map[string]any
	if err := json.Unmarshal(b, &round); err != nil {
		t.Fatal(err)
	}
	fs := round["findings"].([]any)
	if fs[1].(map[string]any)["pyramid"] != nil {
		t.Errorf("finding[1].pyramid should be JSON null, got %v", fs[1].(map[string]any)["pyramid"])
	}
}

func TestBuildValidateResultCleanIsEmptyArray(t *testing.T) {
	res := buildValidateResult("clean.svs", &opentile.Report{Format: opentile.Format("svs")})
	if !res.OK {
		t.Errorf("clean report OK = false, want true")
	}
	if res.Findings == nil {
		t.Errorf("Findings is nil; must be non-nil so JSON renders [] not null")
	}
	b, _ := json.Marshal(res)
	var round map[string]any
	_ = json.Unmarshal(b, &round)
	if _, ok := round["findings"].([]any); !ok {
		t.Errorf("findings did not marshal as a JSON array: %s", b)
	}
}

func TestRenderValidateTextClean(t *testing.T) {
	res := buildValidateResult("good.svs", &opentile.Report{Format: opentile.Format("svs")})
	var b bytes.Buffer
	if err := renderValidateText(&b, &res, false); err != nil {
		t.Fatal(err)
	}
	want := "good.svs · svs · valid\n"
	if b.String() != want {
		t.Errorf("got %q, want %q", b.String(), want)
	}
}

func TestRenderValidateTextFindings(t *testing.T) {
	report := &opentile.Report{
		Format: opentile.Format("svs"),
		Findings: []opentile.Finding{
			{Severity: opentile.Error, Code: "tile-grid-mismatch", Message: "grid 4x4 != 5x4", Pyramid: 0, Level: 3, Count: 200},
			{Severity: opentile.Warning, Code: "missing-metadata", Message: "no mpp", Pyramid: -1, Level: -1, Count: 1},
		},
	}
	res := buildValidateResult("bad.svs", report)
	var b bytes.Buffer
	if err := renderValidateText(&b, &res, true); err != nil {
		t.Fatal(err)
	}
	want := "bad.svs · svs · INVALID (2 findings)\n" +
		"  [error] tile-grid-mismatch  P0/L3 ×200  grid 4x4 != 5x4\n" +
		"  [warning] missing-metadata  no mpp\n"
	if b.String() != want {
		t.Errorf("got:\n%q\nwant:\n%q", b.String(), want)
	}
}

func TestRenderValidateTextWarningPassedGate(t *testing.T) {
	report := &opentile.Report{
		Format: opentile.Format("svs"),
		Findings: []opentile.Finding{
			{Severity: opentile.Warning, Code: "missing-metadata", Message: "no mpp", Pyramid: 0, Level: 0, Count: 1},
		},
	}
	res := buildValidateResult("warn.svs", report)
	var b bytes.Buffer
	// failed=false: warnings present but gate not crossed (lenient mode).
	if err := renderValidateText(&b, &res, false); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got[:len("warn.svs · svs · OK (1 findings)")] != "warn.svs · svs · OK (1 findings)" {
		t.Errorf("header verb wrong, got %q", got)
	}
}

// exitCode extracts the process exit code from a runBin error: 0 for nil, the
// real code for an *exec.ExitError, and -1 for any other (non-exit) error.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func TestValidateGoodSlideExitsZero(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out, err := runBin(bin, "validate", src)
	if code := exitCode(err); code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(string(out), "valid") {
		t.Errorf("output missing 'valid':\n%s", out)
	}
}

func TestValidateGoodSlideJSON(t *testing.T) {
	bin := stripedBinary(t)
	src := filepath.Join(testDir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	out, err := runBin(bin, "validate", "--json", src)
	if code := exitCode(err); code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	var res struct {
		OK       bool   `json:"ok"`
		Format   string `json:"format"`
		Findings []any  `json:"findings"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if !res.OK {
		t.Errorf("expected ok=true for a good slide, got:\n%s", out)
	}
}

func TestValidateMissingPathExitsOne(t *testing.T) {
	bin := stripedBinary(t)
	out, err := runBin(bin, "validate", filepath.Join(t.TempDir(), "does-not-exist.svs"))
	if code := exitCode(err); code != 1 {
		t.Fatalf("exit = %d, want 1 (operational error)\n%s", code, out)
	}
	if !strings.Contains(string(out), "error:") {
		t.Errorf("expected 'error:' on stderr for a missing path:\n%s", out)
	}
}

func TestValidateGarbageFileExitsTwo(t *testing.T) {
	bin := stripedBinary(t)
	junk := filepath.Join(t.TempDir(), "garbage.svs")
	if err := os.WriteFile(junk, []byte("not a tiff at all, just bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runBin(bin, "validate", junk)
	if code := exitCode(err); code != 2 {
		t.Fatalf("exit = %d, want 2 (invalid file)\n%s", code, out)
	}
	if !strings.Contains(string(out), "unopenable") {
		t.Errorf("expected an 'unopenable' finding:\n%s", out)
	}
}
