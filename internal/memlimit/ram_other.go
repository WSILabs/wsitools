//go:build !darwin && !linux

package memlimit

import "errors"

// ErrRAMUnknown is returned by PhysicalRAM on platforms without a probe.
var ErrRAMUnknown = errors.New("memlimit: physical RAM detection unsupported on this platform")

// PhysicalRAM is unsupported here; callers fall back to no soft limit.
func PhysicalRAM() (uint64, error) {
	return 0, ErrRAMUnknown
}
