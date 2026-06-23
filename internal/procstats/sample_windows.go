//go:build windows

package procstats

import (
	"syscall"
	"time"
	"unsafe"
)

// Windows 经 LazyDLL 调用：CPU 时间用 kernel32!GetProcessTimes，常驻内存用
// psapi!GetProcessMemoryInfo 的 WorkingSetSize，系统总内存用 kernel32!GlobalMemoryStatusEx。
var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	procGetProcessTimes      = kernel32.NewProc("GetProcessTimes")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
	psapi                    = syscall.NewLazyDLL("psapi.dll")
	procGetProcessMemoryInfo = psapi.NewProc("GetProcessMemoryInfo")
)

// 当前进程伪句柄恒为 (HANDLE)-1。
const currentProcessHandle = ^uintptr(0)

// filetime 对应 Windows FILETIME（100ns 为单位的 64 位时间）。
type filetime struct {
	low  uint32
	high uint32
}

func (f filetime) duration() time.Duration {
	ticks := int64(f.high)<<32 | int64(f.low)
	return time.Duration(ticks) * 100 // 每 tick = 100ns
}

// processMemoryCounters 对应 PROCESS_MEMORY_COUNTERS（64 位下 SIZE_T 为 uintptr）。
type processMemoryCounters struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
}

// memoryStatusEx 对应 MEMORYSTATUSEX。
type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

func sampleProcess() (time.Duration, uint64) {
	var creation, exit, kernel, user filetime
	procGetProcessTimes.Call(currentProcessHandle,
		uintptr(unsafe.Pointer(&creation)),
		uintptr(unsafe.Pointer(&exit)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)))
	cpu := kernel.duration() + user.duration()

	var pmc processMemoryCounters
	pmc.cb = uint32(unsafe.Sizeof(pmc))
	procGetProcessMemoryInfo.Call(currentProcessHandle, uintptr(unsafe.Pointer(&pmc)), uintptr(pmc.cb))
	return cpu, uint64(pmc.workingSetSize)
}

func systemTotalMem() uint64 {
	var ms memoryStatusEx
	ms.length = uint32(unsafe.Sizeof(ms))
	procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&ms)))
	return ms.totalPhys
}
