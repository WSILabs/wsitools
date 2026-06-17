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
