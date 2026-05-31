// Package memlimit sets a soft Go memory limit (runtime/debug.SetMemoryLimit)
// so memory-heavy commands degrade under GC pressure instead of OOM-ing the
// host. Default = 75% of physical RAM; overridable via the --max-memory flag
// or the GOMEMLIMIT environment variable.
package memlimit

import (
	"fmt"
	"math"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

// Unlimited is the sentinel for "no soft limit" (Go's effective default).
const Unlimited int64 = math.MaxInt64

// Source labels (human-readable, used in reporting).
const (
	SourceFlag    = "--max-memory flag"
	SourceEnv     = "GOMEMLIMIT env"
	SourceDefault = "default (75% of RAM)"
	SourceUnset   = "unset (RAM undetectable)"
)

// Result describes the memory-limit decision for reporting.
type Result struct {
	LimitBytes int64  // limit set or to be set; Unlimited when SourceEnv (see RawEnv) or no cap available
	Source     string // one of the Source* constants
	RAMBytes   uint64 // 0 when undetectable
	Applied    bool   // false when we deliberately left the runtime's value alone
	RawEnv     string // raw GOMEMLIMIT value when SourceEnv; else ""
}

// Resolve implements the precedence flag > GOMEMLIMIT env > 75% default.
// It is pure: all inputs are injected, no syscalls, no runtime mutation.
//   flagVal  — raw --max-memory value ("" if unset)
//   envVal   — value of GOMEMLIMIT ("" if unset)
//   ramBytes — physical RAM in bytes, 0 if undetectable
func Resolve(flagVal, envVal string, ramBytes uint64) (Result, error) {
	if strings.TrimSpace(flagVal) != "" {
		n, err := parseMaxMemory(flagVal)
		if err != nil {
			return Result{}, err
		}
		return Result{LimitBytes: n, Source: SourceFlag, RAMBytes: ramBytes, Applied: true}, nil
	}
	if strings.TrimSpace(envVal) != "" {
		// The runtime already consumed GOMEMLIMIT; do not re-set it. Report
		// the raw string (Go's size grammar differs from --max-memory's).
		return Result{LimitBytes: Unlimited, Source: SourceEnv, RAMBytes: ramBytes,
			Applied: false, RawEnv: envVal}, nil
	}
	if ramBytes > 0 {
		// 75% of RAM. Integer-divide first to avoid overflow in the
		// uint64→int64 product; clamp absurd values (impossible on real
		// hardware) so the *3 cannot wrap int64 negative.
		const maxSafeRAM = uint64(math.MaxInt64) / 3 * 4 // ~10.67 EiB
		if ramBytes > maxSafeRAM {
			ramBytes = maxSafeRAM
		}
		limit := int64(ramBytes/4) * 3
		return Result{LimitBytes: limit, Source: SourceDefault, RAMBytes: ramBytes, Applied: true}, nil
	}
	return Result{LimitBytes: Unlimited, Source: SourceUnset, RAMBytes: 0, Applied: false}, nil
}

// parseMaxMemory parses a --max-memory value. Bare number = MiB. Suffixes
// MB/GB (decimal) and MiB/GiB (binary), case-insensitive. off/none/0 =
// Unlimited. Sized values must be > 0.
func parseMaxMemory(s string) (int64, error) {
	t := strings.ToLower(strings.TrimSpace(s))
	switch t {
	case "off", "none", "0":
		return Unlimited, nil
	}
	mult := float64(1 << 20) // bare number defaults to MiB
	switch {
	case strings.HasSuffix(t, "gib"):
		mult, t = 1<<30, strings.TrimSuffix(t, "gib")
	case strings.HasSuffix(t, "mib"):
		mult, t = 1<<20, strings.TrimSuffix(t, "mib")
	case strings.HasSuffix(t, "gb"):
		mult, t = 1e9, strings.TrimSuffix(t, "gb")
	case strings.HasSuffix(t, "mb"):
		mult, t = 1e6, strings.TrimSuffix(t, "mb")
	}
	val, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
	if err != nil {
		return 0, fmt.Errorf("invalid --max-memory %q: not a number (try 8000, 12GiB, or off)", s)
	}
	if val <= 0 {
		return 0, fmt.Errorf("invalid --max-memory %q: must be > 0 (use 'off' for unlimited)", s)
	}
	return int64(val * mult), nil
}

// Apply resolves the limit from the flag, the GOMEMLIMIT env, and physical
// RAM, then sets it via debug.SetMemoryLimit when the env path did not
// already own it. Returns the Result for reporting.
func Apply(flagVal string) (Result, error) {
	ram, _ := PhysicalRAM() // 0 on error → Resolve falls through to unset
	res, err := Resolve(flagVal, os.Getenv("GOMEMLIMIT"), ram)
	if err != nil {
		return Result{}, err
	}
	if res.Applied {
		debug.SetMemoryLimit(res.LimitBytes)
	}
	return res, nil
}
