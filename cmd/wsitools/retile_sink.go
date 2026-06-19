package main

// flooredLevelCount returns the number of octave levels (each half the previous,
// ceil-halving) from native w×h down to and including the first level whose
// smaller dimension is ≤ tile. Always ≥ 1. This floors the engine's octave
// pyramid at a normal WSI bottom (a thumbnail-sized level) rather than 1×1.
func flooredLevelCount(w, h, tile int) int {
	n := 1
	for min2(w, h) > tile {
		w = (w + 1) / 2
		h = (h + 1) / 2
		n++
	}
	return n
}

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}
