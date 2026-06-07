package main

import "github.com/wsilabs/wsitools/internal/tiff/streamwriter"

// omeEditPlan parameterizes associated-image output. At most one of
// remove/replace is set; empty plan writes all verbatim; dropAll writes none.
type omeEditPlan struct {
	remove  string
	replace string
	spec    *streamwriter.StrippedSpec
	dropAll bool
}
