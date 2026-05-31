//go:build darwin

package memlimit

import "golang.org/x/sys/unix"

// PhysicalRAM returns total physical memory in bytes via the
// hw.memsize sysctl.
func PhysicalRAM() (uint64, error) {
	return unix.SysctlUint64("hw.memsize")
}
