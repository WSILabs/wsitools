//go:build windows

package memlimit

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// memoryStatusEx mirrors the Win32 MEMORYSTATUSEX structure (kernel32).
// golang.org/x/sys/windows does not wrap GlobalMemoryStatusEx, so we call it
// directly via a lazily-resolved kernel32 proc. Field order/sizes must match the
// C layout exactly; the two uint32s pack into the first 8 bytes, then eight
// uint64s follow (naturally 8-aligned).
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

var (
	modkernel32              = windows.NewLazySystemDLL("kernel32.dll")
	procGlobalMemoryStatusEx = modkernel32.NewProc("GlobalMemoryStatusEx")
)

// PhysicalRAM returns total physical memory in bytes via GlobalMemoryStatusEx.
// Without this, Windows falls through to ram_other.go and the memlimit 75%
// default is silently inoperative (wsitools#39).
func PhysicalRAM() (uint64, error) {
	var m memoryStatusEx
	m.Length = uint32(unsafe.Sizeof(m))
	// GlobalMemoryStatusEx returns nonzero on success; the returned err is the
	// thread's last-error, only meaningful when the call reports failure.
	r1, _, err := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m)))
	if r1 == 0 {
		return 0, err
	}
	return m.TotalPhys, nil
}
