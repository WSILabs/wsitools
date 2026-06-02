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

	// Physical scale, emitted on L0 when > 0: XResolution/YResolution
	// (derived from MPP, pixels-per-cm) + ResolutionUnit, and the WSI
	// private tags WSIMPPx/WSIMPPy/WSIMagnification.
	MPPX          float64
	MPPY          float64
	Magnification float64

	// ICCProfile is the embedded color profile, emitted on L0 as tag
	// 34675 (UNDEFINED) when non-empty.
	ICCProfile []byte

	// ImageDepth, when > 0, is emitted on L0 as tag 32997 (LONG). Genuine
	// Aperio writes 1. wsitools only produces 2D output.
	ImageDepth uint32

	// YCbCrSubSampling, when len == 2, is emitted on L0 as tag 530
	// (SHORT[2]). Only meaningful for JPEG-compressed output; the caller
	// supplies the value that matches the JPEG bytes actually written.
	YCbCrSubSampling []uint16

	// DefaultOrder is the tile emission strategy for levels that don't
	// override via LevelSpec.Order. nil → RowMajor (which is the
	// universal default for all writer variants).
	DefaultOrder tileorder.OrderStrategy

	// DefaultReorderCapacity sets the per-level reorder buffer
	// capacity. 0 → max(8, 2*workers) computed at AddLevel time.
	// Debug knob; CLI does not expose this.
	DefaultReorderCapacity uint32

	// FormatName identifies the target file format for tile-order
	// validation: "svs", "tiff", "ome-tiff", "cog-wsi". Empty defaults
	// to "tiff" (permissive: accepts all strategies).
	FormatName string

	// AcceptedOrders restricts which tile-order strategies are
	// permitted at AddLevel time. Empty means "all registered
	// strategies allowed" (the "tiff" default). For SVS callers
	// should set this to ["row-major"].
	AcceptedOrders []string
}
