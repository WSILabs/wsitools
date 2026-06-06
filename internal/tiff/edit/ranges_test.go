package edit

import (
	"errors"
	"testing"
)

func TestRangeMapAddOverlap(t *testing.T) {
	var m RangeMap
	if err := m.Add(Range{Start: 0, End: 10, Owner: 0, What: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Add(Range{Start: 5, End: 15, Owner: 1, What: "b"}); !errors.Is(err, ErrOverlap) {
		t.Fatalf("want ErrOverlap, got %v", err)
	}
	if err := m.Add(Range{Start: 10, End: 20, Owner: 1, What: "c"}); err != nil {
		t.Fatalf("adjacent non-overlap should add: %v", err)
	}
}

func TestRangeMapDominanceQueries(t *testing.T) {
	var m RangeMap
	_ = m.Add(Range{Start: 100, End: 200, Owner: 0, What: "ifd0-strip"})
	_ = m.Add(Range{Start: 200, End: 260, Owner: 0, What: "ifd0-rec"})
	_ = m.Add(Range{Start: 300, End: 360, Owner: 1, What: "ifd1-rec"})
	_ = m.Add(Range{Start: 360, End: 420, Owner: 2, What: "ifd2-rec"})

	if got := m.MinOffsetOfOwnersAtOrAfter(1); got != 300 {
		t.Errorf("MinOffsetOfOwnersAtOrAfter(1) = %d, want 300", got)
	}
	if _, ok := m.AnyRangeOfOwnerAtOrAfter(0, 300); ok {
		t.Errorf("IFD0 should own nothing >= 300")
	}
	_ = m.Add(Range{Start: 500, End: 520, Owner: 0, What: "ifd0-late"})
	if _, ok := m.AnyRangeOfOwnerAtOrAfter(0, 300); !ok {
		t.Errorf("IFD0 owns 500 >= 300, must be reported")
	}
}
