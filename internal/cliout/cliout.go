// Package cliout holds shared text/JSON dual-rendering helpers used by the
// wsitools read-side subcommands (info, dump-ifds, extract, hash, region,
// validate).
package cliout

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"
)

// RegisterJSONFlag binds --json on cmd and returns a pointer to read.
// Subcommands call this in init() and consume *flag in RunE.
func RegisterJSONFlag(cmd *cobra.Command) *bool {
	var jsonMode bool
	cmd.Flags().BoolVar(&jsonMode, "json", false,
		"emit JSON instead of human-readable text")
	return &jsonMode
}

// Render dispatches to human (text) or machine (JSON) based on jsonMode.
// human writes free-form text to w; machine is a JSON-encodable struct.
func Render(jsonMode bool, w io.Writer, human func(io.Writer) error, machine any) error {
	if jsonMode {
		return JSON(w, machine)
	}
	return human(w)
}

// JSON marshals v to indented JSON and writes to w with a trailing newline.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
