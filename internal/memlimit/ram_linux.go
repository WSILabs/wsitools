//go:build linux

package memlimit

import "golang.org/x/sys/unix"

// PhysicalRAM returns total physical memory in bytes via sysinfo(2).
func PhysicalRAM() (uint64, error) {
	var si unix.Sysinfo_t
	if err := unix.Sysinfo(&si); err != nil {
		return 0, err
	}
	return uint64(si.Totalram) * uint64(si.Unit), nil
}
