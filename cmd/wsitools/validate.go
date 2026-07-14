package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/wsilabs/wsitools/internal/cliout"
	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff"

	opentile "github.com/wsilabs/opentile-go"
	otdecoder "github.com/wsilabs/opentile-go/decoder"
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

// probeUndecodableL0 opens the slide and decodes its L0 (0,0) tile as a
// readability probe. It returns (reason, true) when the file cannot be decoded —
// unopenable, no levels, or a tile codec this reader doesn't support (opentile
// "codec not registered … tag N") — and ("", false) when the tile decodes. One
// small tile is cheap and is the definitive "readable by this tool" signal. (#43)
func probeUndecodableL0(path string) (string, bool) {
	src, err := source.Open(path)
	if err != nil {
		return fmt.Sprintf("structurally valid but not openable for decode: %v", err), true
	}
	defer src.Close()
	levels := src.Levels()
	if len(levels) == 0 {
		return "no pyramid levels to decode", true
	}
	if _, err := levels[0].DecodedTile(0, 0); err != nil {
		return fmt.Sprintf("L0 tile (0,0) is not decodable (unsupported/unregistered tile codec?): %v", err), true
	}
	return "", false
}

// probeColorspaceMismatchL0 checks, for a JPEG 2000 L0 tagged with an Aperio
// color code (33003 YCbCr / 33005 RGB), whether that code matches the
// codestream's DECODED colorspace. A mismatch is the wsitools#44 class of bug:
// RGB tiles tagged 33003 make Aperio-family readers (OpenSlide/QuPath) apply a
// wrong YCbCr→RGB conversion. Returns (message, true) only on a CLEAR mismatch;
// any error, a non-Aperio-coded tile, or an ambiguous codestream (no colorspace
// box) yields ("", false) — no false positives. The MCT transform is inverted on
// decode, so an ICT/RCT codestream decodes to RGB.
func probeColorspaceMismatchL0(path string) (string, bool) {
	recs, err := source.WalkIFDs(path)
	if err != nil || len(recs) == 0 {
		return "", false
	}
	tag := uint16(recs[0].Compression)
	if tag != tiff.CompressionJPEG2000 && tag != tiff.CompressionJPEG2000RGB {
		return "", false // only the Aperio YCbCr/RGB codes assert a colorspace
	}
	src, err := source.Open(path)
	if err != nil {
		return "", false
	}
	defer src.Close()
	levels := src.Levels()
	if len(levels) == 0 {
		return "", false
	}
	buf := make([]byte, levels[0].TileMaxSize())
	n, err := levels[0].TileInto(0, 0, buf)
	if err != nil || n == 0 {
		return "", false
	}
	fac, ok := otdecoder.Get("jpeg2000")
	if !ok {
		return "", false
	}
	insp, ok := fac.(otdecoder.CodestreamInspector)
	if !ok {
		return "", false
	}
	info, err := insp.Inspect(buf[:n])
	if err != nil {
		return "", false
	}
	// Only RGB vs YCbCr are decisive for the Aperio color-code check; grayscale /
	// unknown / ambiguous (blank) codestreams don't assert a mismatch.
	switch effectiveColorspace(info.ColorEncoding) {
	case "RGB":
		if tag == tiff.CompressionJPEG2000 {
			return "L0 tiles are tagged JPEG 2000 33003 (Aperio YCbCr) but the codestream decodes to RGB — " +
				"Aperio-family readers will apply a wrong YCbCr→RGB conversion; the RGB code is 33005", true
		}
	case "YCbCr":
		if tag == tiff.CompressionJPEG2000RGB {
			return "L0 tiles are tagged JPEG 2000 33005 (Aperio RGB) but the codestream decodes to YCbCr — the YCbCr code is 33003", true
		}
	}
	return "", false
}

// effectiveColorspace maps a codec-domain ColorEncoding to the EFFECTIVE
// (decoded) colorspace a reader sees: "RGB", "YCbCr", "grayscale", or "" when
// it can't be determined. A JPEG 2000 MCT (ICT/RCT) codestream reports "RGB"
// because the decorrelating transform is inverted on decode. Shared by
// validate's #44 colorspace-mismatch check and info's per-level quality block.
func effectiveColorspace(ce otdecoder.ColorEncoding) string {
	switch ce {
	case otdecoder.ColorRGB, otdecoder.ColorYBRICT, otdecoder.ColorYBRRCT:
		return "RGB"
	case otdecoder.ColorYCbCr:
		return "YCbCr"
	case otdecoder.ColorGrayscale:
		return "grayscale"
	default:
		return ""
	}
}

func runValidate(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	// Cobra's "Error: ..." print is silenced at the root (main owns error output).
	// The exit-2 validation-failure path returns the errValidationFailed sentinel,
	// which main.go maps to exit 2 without printing an "error:" line.
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

	// Structural validity is necessary but not sufficient: a file can have
	// well-formed TIFF offsets/tags yet carry tiles in a codec this reader can't
	// decode (e.g. htj2k tiles written into an SVS), so region/hash fail while
	// validate would report "valid" — false assurance. When the structural report
	// passed, probe-decode an L0 tile; if it can't be decoded, add an error
	// finding so "valid" implies "readable by this tool". (#43)
	if !failed {
		if msg, bad := probeUndecodableL0(path); bad {
			result.Findings = append(result.Findings, validateFinding{
				Severity: opentile.Error.String(),
				Code:     "undecodable-tile",
				Message:  msg,
				Count:    1,
			})
			result.OK = false
			result.Worst = opentile.Error.String()
			failed = true
		} else if msg, bad := probeColorspaceMismatchL0(path); bad {
			// Warning, not error: the file decodes fine here, but its declared
			// colorspace code is wrong, so Aperio-family readers render wrong
			// colors (wsitools#44 class). Fails only under --strict.
			result.Findings = append(result.Findings, validateFinding{
				Severity: opentile.Warning.String(),
				Code:     "colorspace-mismatch",
				Message:  msg,
				Count:    1,
			})
			result.Worst = opentile.Warning.String()
			if validateStrict {
				failed = true
			}
		}
	}

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
