//go:build linux

package procstats

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// userHZ 是 Linux 时钟节拍频率（_SC_CLK_TCK）。内核几乎恒为 100，无需 cgo sysconf。
const userHZ = 100

// sampleProcess 读 /proc/self/stat 取进程 CPU 时间与常驻内存。
func sampleProcess() (time.Duration, uint64) {
	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0, 0
	}
	s := string(data)
	// comm（第 2 字段）可能含空格与括号，按最后一个 ')' 之后再分词。
	rparen := strings.LastIndexByte(s, ')')
	if rparen < 0 || rparen+2 > len(s) {
		return 0, 0
	}
	// 切片以 state（第 3 字段）为 fields[0]：
	// utime=第14字段→fields[11]，stime=第15字段→fields[12]，rss(页)=第24字段→fields[21]。
	fields := strings.Fields(s[rparen+2:])
	if len(fields) < 22 {
		return 0, 0
	}
	utime, _ := strconv.ParseInt(fields[11], 10, 64)
	stime, _ := strconv.ParseInt(fields[12], 10, 64)
	rssPages, _ := strconv.ParseInt(fields[21], 10, 64)

	cpu := time.Duration((utime + stime) * int64(time.Second) / userHZ)
	rss := uint64(rssPages) * uint64(os.Getpagesize())
	return cpu, rss
}

// systemTotalMem 读 /proc/meminfo 的 MemTotal（kB）。
func systemTotalMem() uint64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "MemTotal:"); ok {
			f := strings.Fields(rest)
			if len(f) >= 1 {
				kb, _ := strconv.ParseUint(f[0], 10, 64)
				return kb * 1024
			}
		}
	}
	return 0
}
