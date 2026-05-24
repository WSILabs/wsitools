// Package tileorder defines OrderStrategy: a strategy that maps tile grid
// coordinates (x, y) within a single pyramid level to a linear emission
// index. Used by the streamwriter reorder buffer to make output bytes
// deterministic across runs, and by cogwsiwriter's finalize pass to walk
// the tile spool in a chosen order.
//
// Strategies operate within a single level only. Cross-level ordering is
// format-mandated (SVS: largest-first; COG-WSI: overview-first) and
// managed by the caller's loop over levels.
//
// Shipped strategies: RowMajor (universal default), HilbertCurve (better
// 2-D locality for cloud range reads), Morton (Z-order; cheap to compute,
// weaker locality than Hilbert).
package tileorder
