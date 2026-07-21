//go:build !windows && !linux

package procstats

import (
	"syscall"
	"time"
)

// 非 Windows/Linux（macOS、*BSD 等）兜底：CPU 时间用 getrusage，常驻内存用其 Maxrss
// 近似（darwin 为字节，部分 BSD 为 KB；作为次要平台的粗略读数）。系统总内存难以
// 无 cgo 取得，返回 0——GUI 端对 0 会忽略系统占比展示。
func sampleProcess() (time.Duration, uint64) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, 0
	}
	cpu := timevalDuration(ru.Utime) + timevalDuration(ru.Stime)
	return cpu, uint64(ru.Maxrss)
}

func timevalDuration(tv syscall.Timeval) time.Duration {
	return time.Duration(tv.Sec)*time.Second + time.Duration(tv.Usec)*time.Microsecond
}

func systemTotalMem() uint64 { return 0 }
