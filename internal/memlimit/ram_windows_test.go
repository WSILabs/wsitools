//go:build windows

package memlimit

import "testing"

// TestPhysicalRAMWindows guards wsitools#39: Windows had no PhysicalRAM probe, so
// the memlimit 75%-of-RAM default was silently inoperative. GlobalMemoryStatusEx
// must return a plausible nonzero total. Runs only on the Windows CI unit job.
func TestPhysicalRAMWindows(t *testing.T) {
	n, err := PhysicalRAM()
	if err != nil {
		t.Fatalf("PhysicalRAM: %v", err)
	}
	if n < 1<<30 { // any real machine has > 1 GiB physical RAM
		t.Errorf("PhysicalRAM = %d bytes, want a plausible total (> 1 GiB)", n)
	}
}
