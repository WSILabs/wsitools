// Package retile is the streaming retile engine: one opentile ScaledStrips
// iterator feeds a chain of per-level builders (finest→coarsest, fixed 2× box
// descent) that emit RGB tiles to a worker-pool encoder, whose compressed
// output is handed to a serialized sink. The engine is codec- and
// container-agnostic — callers supply a TileEncoder (RGB→bytes) and a TileSink
// (routes WriteTile(level,col,row) to a writer). It generalizes the DZI
// pyramid-descent that originally lived in cmd/wsitools/convert_dzi_descent.go.
//
// Level numbering: the engine works finest-first with engine-relative index k
// (k=0 = finest). Sinks translate k to their container's numbering.
package retile
