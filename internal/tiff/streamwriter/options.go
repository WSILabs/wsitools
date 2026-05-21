package streamwriter

import (
	"time"

	"github.com/cornish/wsitools/internal/tiff"
)

// Options configures a new Writer.
type Options struct {
	BigTIFF tiff.BigTIFFMode

	// Standard TIFF metadata tags, emitted on L0 when set.
	ImageDescription string
	Make             string
	Model            string
	Software         string
	DateTime         time.Time

	// wsitools private tags emitted on L0 when set.
	SourceFormat string
	ToolsVersion string
}
