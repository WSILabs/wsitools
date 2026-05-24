package streamwriter

import (
	"time"

	"github.com/wsilabs/wsitools/internal/tiff"
	"github.com/wsilabs/wsitools/internal/tiff/tileorder"
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

	// DefaultOrder is the tile emission strategy for levels that don't
	// override via LevelSpec.Order. nil → RowMajor (which is the
	// universal default for all writer variants).
	DefaultOrder tileorder.OrderStrategy

	// DefaultReorderCapacity sets the per-level reorder buffer
	// capacity. 0 → max(8, 2*workers) computed at AddLevel time.
	// Debug knob; CLI does not expose this.
	DefaultReorderCapacity uint32
}
