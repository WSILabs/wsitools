package tiff

import (
	"math"
	"testing"
)

func TestMPPToResolution(t *testing.T) {
	num, denom := MPPToResolution(0.25)
	if denom == 0 {
		t.Fatal("denom = 0")
	}
	got := float64(num) / float64(denom)
	if math.Abs(got-40000) > 1 {
		t.Errorf("MPPToResolution(0.25) = %d/%d = %g px/cm, want ~40000", num, denom, got)
	}
	recovered := 10000.0 / got
	if math.Abs(recovered-0.25)/0.25 > 0.001 {
		t.Errorf("round-trip MPP = %g, want ~0.25", recovered)
	}
}

func TestMPPToResolutionUnknown(t *testing.T) {
	for _, mpp := range []float64{0, -1} {
		if n, d := MPPToResolution(mpp); n != 0 || d != 0 {
			t.Errorf("MPPToResolution(%g) = %d/%d, want 0/0", mpp, n, d)
		}
	}
}

func TestMPPToResolutionNoOverflow(t *testing.T) {
	for _, mpp := range []float64{0.5, 0.25, 0.1, 0.06, 0.001} {
		num, denom := MPPToResolution(mpp)
		if num == 0 || denom == 0 {
			t.Errorf("MPPToResolution(%g) = %d/%d, want nonzero", mpp, num, denom)
		}
		got := float64(num) / float64(denom)
		want := 10000.0 / mpp
		if math.Abs(got-want)/want > 0.01 {
			t.Errorf("MPPToResolution(%g) = %g px/cm, want ~%g", mpp, got, want)
		}
	}
}
