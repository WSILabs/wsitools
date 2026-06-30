package main

import (
	"testing"

	"github.com/wsilabs/wsitools/internal/retile"
)

// Compile-time: both M2 sinks satisfy retileSink.
var _ retileSink = (*streamwriterSink)(nil)
var _ retileSink = (*cogwsiSink)(nil)

func TestSumLevelTiles(t *testing.T) {
	// Emitting levels contribute Cols*Rows; Intermediate levels carry Cols=Rows=0.
	levels := []retile.LevelSpec{
		{Cols: 4, Rows: 3},                     // 12
		{Cols: 0, Rows: 0, Intermediate: true}, // 0
		{Cols: 2, Rows: 2},                     // 4
		{Cols: 1, Rows: 1},                     // 1
	}
	if got := sumLevelTiles(levels); got != 17 {
		t.Errorf("sumLevelTiles = %d, want 17", got)
	}
	if got := sumLevelTiles(nil); got != 0 {
		t.Errorf("sumLevelTiles(nil) = %d, want 0", got)
	}
}
