package memlimit

import (
	"math"
	"testing"
)

const gib = 1 << 30
const mib = 1 << 20

func TestResolveFlag(t *testing.T) {
	tests := []struct {
		name      string
		flag      string
		wantLimit int64
		wantErr   bool
	}{
		{"bare-mib", "8000", 8000 * mib, false},
		{"gib-suffix", "12GiB", 12 * gib, false},
		{"gb-suffix", "2GB", 2_000_000_000, false},
		{"mb-suffix", "500mb", 500_000_000, false},
		{"off", "off", math.MaxInt64, false},
		{"none", "none", math.MaxInt64, false},
		{"zero", "0", math.MaxInt64, false},
		{"bogus", "banana", 0, true},
		{"zero-sized", "0mib", 0, true},
		{"negative", "-5", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(tt.flag, "", 16*gib)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Resolve(%q) expected error, got %+v", tt.flag, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve(%q) unexpected error: %v", tt.flag, err)
			}
			if got.LimitBytes != tt.wantLimit {
				t.Errorf("LimitBytes = %d, want %d", got.LimitBytes, tt.wantLimit)
			}
			if got.Source != SourceFlag {
				t.Errorf("Source = %q, want %q", got.Source, SourceFlag)
			}
			if !got.Applied {
				t.Errorf("Applied = false, want true")
			}
		})
	}
}

func TestResolveEnv(t *testing.T) {
	got, err := Resolve("", "5GiB", 16*gib)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Source != SourceEnv {
		t.Errorf("Source = %q, want %q", got.Source, SourceEnv)
	}
	if got.Applied {
		t.Errorf("Applied = true, want false (env path must not set the limit)")
	}
	if got.RawEnv != "5GiB" {
		t.Errorf("RawEnv = %q, want %q", got.RawEnv, "5GiB")
	}
}

func TestResolveDefault(t *testing.T) {
	got, err := Resolve("", "", 16*gib)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.LimitBytes != 12*gib {
		t.Errorf("LimitBytes = %d, want %d (75%% of 16GiB)", got.LimitBytes, 12*gib)
	}
	if got.Source != SourceDefault || !got.Applied {
		t.Errorf("got Source=%q Applied=%v, want default/true", got.Source, got.Applied)
	}
}

func TestResolveUnset(t *testing.T) {
	got, err := Resolve("", "", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Source != SourceUnset {
		t.Errorf("Source = %q, want %q", got.Source, SourceUnset)
	}
	if got.Applied {
		t.Errorf("Applied = true, want false (nothing to set when RAM unknown)")
	}
	if got.LimitBytes != math.MaxInt64 {
		t.Errorf("LimitBytes = %d, want Unlimited", got.LimitBytes)
	}
}

func TestResolveFlagBeatsEnv(t *testing.T) {
	got, err := Resolve("4GiB", "5GiB", 16*gib)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Source != SourceFlag || got.LimitBytes != 4*gib {
		t.Errorf("flag must win: got Source=%q Limit=%d", got.Source, got.LimitBytes)
	}
}

// A whitespace-only flag value must be treated as unset and fall through
// to the env path.
func TestResolveBlankFlagFallsThrough(t *testing.T) {
	got, err := Resolve("   ", "5GiB", 16*gib)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Source != SourceEnv {
		t.Errorf("Source = %q, want %q (blank flag should fall through)", got.Source, SourceEnv)
	}
}

func TestPhysicalRAMSmoke(t *testing.T) {
	ram, err := PhysicalRAM()
	if err != nil {
		t.Skipf("PhysicalRAM unsupported on this platform: %v", err)
	}
	if ram < 1<<30 { // sanity: any dev/CI host has >= 1 GiB
		t.Errorf("PhysicalRAM = %d bytes, implausibly small", ram)
	}
}
