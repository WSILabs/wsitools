# `wsitools validate` Command Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a standalone `wsitools validate <file>` command that reports a slide's structural conformance (per opentile-go's reader) as human or JSON output, with a CI-gateable three-way exit code.

**Architecture:** A thin `cmd/wsitools/validate.go` cobra command wraps `opentile.ValidateFile(path)` — one read-only call, no `internal/source` involvement (so a structurally-broken file produces a report instead of erroring). Pure helpers map the returned `*opentile.Report` to a human line list and a JSON struct; an exit-code threshold helper plus a sentinel error wired into `main.go` give exit 0 (valid) / 2 (invalid) / 1 (operational, e.g. missing path).

**Tech Stack:** Go, cobra, `github.com/wsilabs/opentile-go` v0.45.1 (`ValidateFile`, `Report`, `Finding`, `Severity`, `CheckCode`), `internal/cliout` (shared text/JSON rendering).

**Spec:** `docs/superpowers/specs/2026-06-17-validate-command-design.md`

---

## File Structure

- **Create `cmd/wsitools/validate.go`** — the whole command: cobra command + `init()` registration, `RunE`, the `validateResult`/`validateFinding` JSON structs, pure mapping/formatting helpers (`buildValidateResult`, `locusPtr`, `formatName`, `formatLocus`, `renderValidateText`), the exit-code threshold helper (`reportFails`), and the `errValidationFailed` sentinel.
- **Modify `cmd/wsitools/main.go`** — add one `errors.Is(err, errValidationFailed)` → `os.Exit(2)` branch next to the existing `context.Canceled` branch.
- **Create `cmd/wsitools/validate_test.go`** — unit tests (pure helpers, no fixtures) + integration tests (gated by the binary + `WSI_TOOLS_TESTDIR`).
- **Modify `README.md`, `CLAUDE.md`, `CHANGELOG.md`** — document the command.

Reference facts (verified against the installed module and codebase):
- `opentile.ValidateFile(path string) (*opentile.Report, error)` — error is operational-only (missing/unreadable path); a file that fails to open becomes a `CheckUnopenable` Error **finding** in the returned `*Report`.
- `opentile.Report` has exported fields `Format opentile.Format` and `Findings []opentile.Finding`, plus methods `OK() bool` and `Worst() opentile.Severity`.
- `opentile.Finding` exported fields: `Severity opentile.Severity`, `Code opentile.CheckCode`, `Message string`, `Pyramid int`, `Level int`, `Count int`. `Pyramid`/`Level` are `-1` when not applicable; findings come pre-sorted (Error first).
- `opentile.Severity` constants `Info` < `Warning` < `Error`; `Severity.String()` → `"info"|"warning"|"error"`.
- `opentile.CheckCode` is `string`-underlying; `opentile.Format` is `string`-underlying with `FormatUnknown = ""`.
- `cliout.RegisterJSONFlag(cmd) *bool` and `cliout.Render(jsonMode bool, w io.Writer, human func(io.Writer) error, machine any) error` are the existing dual-render helpers (see `info.go`).
- Test harness helpers already exist: `runBin(bin string, args ...string) ([]byte, error)` (`dicom_testhelpers_test.go`), `stripedBinary(t) string` and `stripedSample(t, rel) string` (`striped_formats_test.go`), `testDir(t) string` (`convert_integration_test.go`). The binary is found at `./bin/wsitools` after `make build`; tests `t.Skip` when it's absent.

---

## Task 1: Pure mapping + threshold helpers

Build the side-effect-free core first: the JSON structs and every pure helper, fully unit-tested without cobra, the binary, or fixtures.

**Files:**
- Create: `cmd/wsitools/validate.go`
- Test: `cmd/wsitools/validate_test.go`

- [ ] **Step 1: Write the failing tests**

Create `cmd/wsitools/validate_test.go`:

```go
package main

import (
	"encoding/json"
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
	if res.OK { // an Error finding -> not OK
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

	// -1 loci must serialize as JSON null, and findings is always an array.
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
```

- [ ] **Step 2: Run the tests to verify they fail to compile**

Run: `go test ./cmd/wsitools/ -run 'TestReportFails|TestLocusPtr|TestFormatName|TestFormatLocus|TestBuildValidateResult' 2>&1 | head`
Expected: build failure — `undefined: reportFails`, `undefined: locusPtr`, etc.

- [ ] **Step 3: Write the minimal implementation**

Create `cmd/wsitools/validate.go` with the structs and pure helpers only (the cobra command and RunE come in Task 3):

```go
package main

import (
	"errors"
	"fmt"
	"io"

	opentile "github.com/wsilabs/opentile-go"
)

// errValidationFailed is the sentinel returned by `validate` when the report
// crosses the failure threshold. main.go maps it to exit code 2 (the report has
// already been printed to stdout), keeping it distinct from an operational
// error (exit 1).
var errValidationFailed = errors.New("validation failed")

// validateFinding is the JSON-facing shape of one opentile.Finding. Pyramid and
// Level are pointers so a not-applicable (-1) locus serializes as JSON null.
type validateFinding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Pyramid  *int   `json:"pyramid"`
	Level    *int   `json:"level"`
	Count    int    `json:"count"`
}

// validateResult is the JSON-facing shape of one validated file.
type validateResult struct {
	Path     string            `json:"path"`
	Format   string            `json:"format"`
	OK       bool              `json:"ok"`
	Worst    string            `json:"worst"`
	Findings []validateFinding `json:"findings"`
}

// reportFails reports whether a report crosses the failure threshold: any Error
// finding always fails; under strict, any Warning also fails. Info never fails.
func reportFails(r *opentile.Report, strict bool) bool {
	if !r.OK() {
		return true
	}
	if strict && r.Worst() >= opentile.Warning {
		return true
	}
	return false
}

// locusPtr maps a coarse locus value to a pointer: -1 (not applicable) -> nil.
func locusPtr(v int) *int {
	if v < 0 {
		return nil
	}
	return &v
}

// formatName renders a detected format, mapping the unknown (empty) format to
// "unknown" so output never shows a blank field.
func formatName(f opentile.Format) string {
	if f == opentile.FormatUnknown {
		return "unknown"
	}
	return string(f)
}

// buildValidateResult maps an opentile.Report to the JSON-facing struct. The
// Findings slice is always non-nil so it renders as [] (not null) for a clean file.
func buildValidateResult(path string, r *opentile.Report) validateResult {
	res := validateResult{
		Path:     path,
		Format:   formatName(r.Format),
		OK:       r.OK(),
		Worst:    r.Worst().String(),
		Findings: make([]validateFinding, 0, len(r.Findings)),
	}
	for _, f := range r.Findings {
		res.Findings = append(res.Findings, validateFinding{
			Severity: f.Severity.String(),
			Code:     string(f.Code),
			Message:  f.Message,
			Pyramid:  locusPtr(f.Pyramid),
			Level:    locusPtr(f.Level),
			Count:    f.Count,
		})
	}
	return res
}

// formatLocus renders the coarse locus + rolled-up count as a compact token,
// e.g. "P0/L3 ×200", "P0", "×4", or "" (whole-file, count 1). A count suffix is
// appended only when count > 1.
func formatLocus(pyramid, level *int, count int) string {
	var loc string
	switch {
	case pyramid != nil && level != nil:
		loc = fmt.Sprintf("P%d/L%d", *pyramid, *level)
	case pyramid != nil:
		loc = fmt.Sprintf("P%d", *pyramid)
	}
	if count > 1 {
		if loc != "" {
			loc += " "
		}
		loc += fmt.Sprintf("×%d", count)
	}
	return loc
}

// renderValidateText writes the human report: a header line whose verb reflects
// the failure-threshold decision (so it never contradicts the exit code), then
// one line per finding. failed is the precomputed reportFails result.
func renderValidateText(w io.Writer, r *validateResult, failed bool) error {
	verb := "valid"
	if len(r.Findings) > 0 {
		if failed {
			verb = fmt.Sprintf("INVALID (%d findings)", len(r.Findings))
		} else {
			verb = fmt.Sprintf("OK (%d findings)", len(r.Findings))
		}
	}
	if _, err := fmt.Fprintf(w, "%s · %s · %s\n", r.Path, r.Format, verb); err != nil {
		return err
	}
	for _, f := range r.Findings {
		loc := formatLocus(f.Pyramid, f.Level, f.Count)
		var err error
		if loc != "" {
			_, err = fmt.Fprintf(w, "  [%s] %s  %s  %s\n", f.Severity, f.Code, loc, f.Message)
		} else {
			_, err = fmt.Fprintf(w, "  [%s] %s  %s\n", f.Severity, f.Code, f.Message)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
```

Note: `renderValidateText` is written now (it's pure and the file needs `io`/`errors`/`fmt` imported once); it gets its own test in Task 2.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./cmd/wsitools/ -run 'TestReportFails|TestLocusPtr|TestFormatName|TestFormatLocus|TestBuildValidateResult' -v 2>&1 | tail -30`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/validate.go cmd/wsitools/validate_test.go
git commit -m "feat(validate): pure report→struct mapping + exit-code threshold helpers"
```

---

## Task 2: Human renderer test

Lock the human output format with a golden-string test.

**Files:**
- Test: `cmd/wsitools/validate_test.go` (add)

- [ ] **Step 1: Write the failing test**

Append to `cmd/wsitools/validate_test.go`:

```go
import "bytes" // add to the existing import block

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
```

- [ ] **Step 2: Run the test to verify it passes**

Run: `go test ./cmd/wsitools/ -run 'TestRenderValidateText' -v 2>&1 | tail -20`
Expected: PASS (the renderer was implemented in Task 1). If a golden string mismatches, fix the **test's** expected string to match the implementation's spacing — do not change the renderer's format.

- [ ] **Step 3: Commit**

```bash
git add cmd/wsitools/validate_test.go
git commit -m "test(validate): golden-string coverage for the human renderer"
```

---

## Task 3: Wire the cobra command + main.go exit-2 sentinel

Add the command, its flags/registration, the `RunE` glue, and the `main.go` branch that turns the sentinel into exit code 2.

**Files:**
- Modify: `cmd/wsitools/validate.go` (add command + RunE)
- Modify: `cmd/wsitools/main.go:127-134`

- [ ] **Step 1: Add the command, flags, and RunE to `validate.go`**

Add these imports to `validate.go`'s import block: `"github.com/spf13/cobra"`, `"github.com/wsilabs/wsitools/internal/cliout"`. Then append:

```go
var (
	validateJSON   *bool
	validateStrict bool
)

var validateCmd = &cobra.Command{
	Use:   "validate <file>",
	Short: "Check a slide's structural conformance (opentile-go reader)",
	Long: `Validate the structure of a whole-slide image against opentile-go's
reader: pyramid level geometry, tile-grid math, monotone downsampling, and
per-format structural checks. Reports findings (info / warning / error) in
human-readable text or, with --json, machine-readable JSON.

Exit codes:
  0  valid       no error findings (and, with --strict, no warnings)
  2  invalid     findings crossed the failure threshold (file is malformed)
  1  error       could not attempt validation (path missing / unreadable)`,
	Args: cobra.ExactArgs(1),
	RunE: runValidate,
}

func init() {
	validateJSON = cliout.RegisterJSONFlag(validateCmd)
	validateCmd.Flags().BoolVar(&validateStrict, "strict", false,
		"treat warning findings as failures (affects exit code only)")
	rootCmd.AddCommand(validateCmd)
}

func runValidate(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	path := args[0]

	// ValidateFile bypasses internal/source on purpose: an open/parse failure
	// becomes a CheckUnopenable finding in the report, not a hard error. Only a
	// genuinely missing/unreadable path returns an operational error here.
	report, err := opentile.ValidateFile(path)
	if err != nil {
		return err
	}

	failed := reportFails(report, validateStrict)
	result := buildValidateResult(path, report)

	if rErr := cliout.Render(*validateJSON, cmd.OutOrStdout(),
		func(w io.Writer) error { return renderValidateText(w, &result, failed) },
		result); rErr != nil {
		return rErr
	}

	if failed {
		return errValidationFailed
	}
	return nil
}
```

- [ ] **Step 2: Add the exit-2 branch to `main.go`**

In `cmd/wsitools/main.go`, the current block (around lines 127-134) is:

```go
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "interrupted")
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
```

Insert the sentinel branch so it becomes:

```go
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "interrupted")
			os.Exit(130)
		}
		if errors.Is(err, errValidationFailed) {
			os.Exit(2) // report already printed by `validate`; no "error:" line
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
```

- [ ] **Step 3: Build and vet**

Run: `go build ./... && go vet ./cmd/wsitools/`
Expected: no output (clean build + vet). `errValidationFailed` is now referenced by both files.

- [ ] **Step 4: Smoke-test the command surface**

Run: `go run ./cmd/wsitools validate --help 2>&1 | head -20`
Expected: usage text including the `--strict` and `--json` flags and the exit-code table.

- [ ] **Step 5: Run the full unit suite for the package**

Run: `go test ./cmd/wsitools/ -run 'Validate|validate' 2>&1 | tail -20`
Expected: PASS (no regressions; integration tests added next).

- [ ] **Step 6: Commit**

```bash
git add cmd/wsitools/validate.go cmd/wsitools/main.go
git commit -m "feat(validate): wire cobra command + main.go exit-2 sentinel"
```

---

## Task 4: Integration tests (real binary, exit codes)

Exercise the built binary end-to-end so exit codes 0/1/2 and the report output are covered against real and crafted inputs.

**Files:**
- Test: `cmd/wsitools/validate_test.go` (add)

- [ ] **Step 1: Write the failing integration tests**

Append to `cmd/wsitools/validate_test.go`. Add `"os"`, `"os/exec"`, `"path/filepath"`, `"strings"`, and `"errors"` to the import block if not already present (Task 1 already imports `errors` indirectly? No — the test file does not yet import `errors`/`os`/`exec`. Add them.):

```go
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
```

- [ ] **Step 2: Build the binary the tests need**

Run: `make build`
Expected: produces `bin/wsitools` (the integration tests `t.Skip` without it).

- [ ] **Step 3: Run the integration tests to verify they pass**

Run: `WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run 'TestValidate(GoodSlide|MissingPath|GarbageFile)' -v 2>&1 | tail -30`
Expected: PASS. (`TestValidateGoodSlide*` skip if the SVS fixture is absent; the missing-path and garbage-file cases always run.)

- [ ] **Step 4: Commit**

```bash
git add cmd/wsitools/validate_test.go
git commit -m "test(validate): end-to-end exit-code coverage (0 valid / 1 missing / 2 invalid)"
```

---

## Task 5: Documentation

Document the new command in the three doc surfaces.

**Files:**
- Modify: `README.md`
- Modify: `CLAUDE.md:4`
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Add the command to README's read-side command list**

In `README.md`, the read-side commands are listed around lines 23-38 (`info`, `dump-ifds`, `region`, `hash`, `extract`). Immediately **after** the `wsitools hash` bullet (the block starting at line 35), insert:

```markdown
- `wsitools validate <file>` — check a slide's structural conformance against
  opentile-go's reader (level geometry, tile-grid math, monotone pyramid,
  per-format checks). Prints findings (info / warning / error) as text or
  `--json`. Exit code: `0` valid, `2` invalid (findings crossed the gate), `1`
  operational error (path missing/unreadable). `--strict` treats warnings as
  failures.
```

- [ ] **Step 2: Add `validate` to CLAUDE.md's CLI summary**

In `CLAUDE.md`, line 4 currently reads:

```
pathology. CLI bundles read-side inspection (`info`, `dump-ifds`, `extract`,
`hash`, `region`), write-side conversion (`convert --to {cog-wsi, dzi,
```

Change the read-side list to include `validate`:

```
pathology. CLI bundles read-side inspection (`info`, `dump-ifds`, `extract`,
`hash`, `region`, `validate`), write-side conversion (`convert --to {cog-wsi, dzi,
```

- [ ] **Step 3: Add a CHANGELOG entry**

In `CHANGELOG.md`, under `## [Unreleased]` → `### Added`, add a new top entry:

```markdown
- **`wsitools validate <file>`** — new read-side command that checks a slide's
  structural conformance using opentile-go v0.45.1's `Validate` API
  (`ValidateFile` → `Report` of findings with severities and check codes).
  Human or `--json` output; three-way exit code (`0` valid / `2` invalid / `1`
  operational error); `--strict` promotes warnings to failures. Calls
  `ValidateFile` directly (not `internal/source`), so a malformed file is
  reported as an `unopenable` finding rather than erroring out.
```

- [ ] **Step 4: Verify docs build/reference nothing broken**

Run: `git diff --stat README.md CLAUDE.md CHANGELOG.md`
Expected: three files changed, additions only.

- [ ] **Step 5: Commit**

```bash
git add README.md CLAUDE.md CHANGELOG.md
git commit -m "docs(validate): document the validate command (README, CLAUDE.md, CHANGELOG)"
```

---

## Final verification

- [ ] **Step 1: Full package unit suite (race)**

Run: `go test ./cmd/wsitools/ -race -run 'Validate|validate' 2>&1 | tail -20`
Expected: PASS. (Per CLAUDE.md, heavy `-race` runs of the whole `cmd/wsitools` package can exceed the default 600s timeout under load; this run is name-filtered so it stays fast.)

- [ ] **Step 2: Full integration suite for the command**

Run: `make build && WSI_TOOLS_TESTDIR=$(pwd)/sample_files go test ./cmd/wsitools/ -run 'TestValidate' -v 2>&1 | tail -30`
Expected: PASS (fixture-dependent cases may SKIP if `sample_files` is absent).

- [ ] **Step 3: Build + vet the whole module**

Run: `go build ./... && go vet ./...`
Expected: clean.

---

## Notes for the implementer

- **Do not route through `internal/source`.** Calling `opentile.ValidateFile` directly is the whole point — it converts open failures into findings. Routing through `source.Open` would make a malformed file exit 1 instead of 2.
- **`×` is a multibyte rune.** The `formatLocus` count suffix uses `×` (U+00D7), matching the golden-string tests; keep the source file UTF-8.
- **Exit-code mapping lives in `main.go`, not the command.** `RunE` only returns the `errValidationFailed` sentinel; `main.go` translates it to `os.Exit(2)`. Don't call `os.Exit` from inside `RunE` (it would bypass the deferred CPU-profile cleanup in `main`).
- **Don't widen scope.** No write-side self-check, no multi-path, no Tier-2 options — those are explicitly deferred in the spec.
```
