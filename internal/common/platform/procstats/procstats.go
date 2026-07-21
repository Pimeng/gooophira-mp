// Package procstats 周期性采样进程 CPU 占用率与内存，为服务端 GUI 的监控图表
// 提供历史曲线回填。对应 TS server/utils/processStats.ts。
//
// 仅依赖标准库、无 CGO：Windows 经 syscall LazyDLL 调用 kernel32/psapi，
// Linux 读 /proc，其它平台用 getrusage 兜底（见各 sample_*.go）。
//
// CPU 百分比为整机口径：(用户态+内核态 CPU 时间增量) / (墙钟时间 × 核心数) × 100，
// 上限 100，保留一位小数——与原版语义一致。
package procstats

import (
	"math"
	"runtime"
	"sync"
	"time"
)

// Sample 是单个采样点。JSON 字段名与 GUI 契约一致（timestamp/cpuPercent/rss/heapUsed/heapTotal）。
type Sample struct {
	Timestamp  int64   `json:"timestamp"`  // Unix 毫秒
	CPUPercent float64 `json:"cpuPercent"` // 整机口径 0-100
	RSS        uint64  `json:"rss"`        // 常驻内存（字节）
	HeapUsed   uint64  `json:"heapUsed"`   // Go 堆已用（字节）
	HeapTotal  uint64  `json:"heapTotal"`  // Go 堆向 OS 申请（字节）
}

const (
	// sampleInterval 是采样周期。
	sampleInterval = 2 * time.Second
	// maxHistory 是历史采样点上限（2s × 300 = 10 分钟）。
	maxHistory = 300
)

// Sampler 周期性采样并维护固定长度的历史环形缓冲。所有导出方法并发安全。
type Sampler struct {
	cpuCount int
	sysTotal uint64

	mu      sync.Mutex
	samples []Sample

	stopCh   chan struct{}
	stopOnce sync.Once

	lastCPU time.Duration
	lastAt  time.Time
}

// Start 创建采样器并立即开始（后台 goroutine，每 2s 采样一次）。
func Start() *Sampler {
	s := &Sampler{
		cpuCount: max(1, runtime.NumCPU()),
		sysTotal: systemTotalMem(),
		stopCh:   make(chan struct{}),
	}
	s.lastCPU, _ = sampleProcess()
	s.lastAt = time.Now()
	s.take() // 首帧立即可用（GUI 打开时无需等待）
	go s.loop()
	return s
}

func (s *Sampler) loop() {
	t := time.NewTicker(sampleInterval)
	defer t.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-t.C:
			s.take()
		}
	}
}

func (s *Sampler) take() {
	now := time.Now()
	cpu, rss := sampleProcess()

	elapsed := now.Sub(s.lastAt)
	used := cpu - s.lastCPU
	s.lastCPU, s.lastAt = cpu, now

	var pct float64
	if elapsed > 0 && used > 0 {
		pct = float64(used) / (float64(elapsed) * float64(s.cpuCount)) * 100
		pct = math.Round(min(pct, 100)*10) / 10
	}

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	if rss == 0 {
		rss = mem.Sys // 平台取不到 RSS 时退化为 Go 运行时向 OS 申请的内存
	}

	sample := Sample{
		Timestamp:  now.UnixMilli(),
		CPUPercent: pct,
		RSS:        rss,
		HeapUsed:   mem.HeapAlloc,
		HeapTotal:  mem.HeapSys,
	}

	s.mu.Lock()
	s.samples = append(s.samples, sample)
	if len(s.samples) > maxHistory {
		s.samples = s.samples[len(s.samples)-maxHistory:]
	}
	s.mu.Unlock()
}

// Current 返回最新采样点；尚无采样时 ok=false。
func (s *Sampler) Current() (Sample, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.samples) == 0 {
		return Sample{}, false
	}
	return s.samples[len(s.samples)-1], true
}

// History 返回按时间顺序的历史采样副本。
func (s *Sampler) History() []Sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Sample, len(s.samples))
	copy(out, s.samples)
	return out
}

// LiveMemory 返回即时内存读数：RSS（进程常驻内存）、HeapUsed、HeapTotal（均为字节）。
// 用于 /admin/metrics 主内存块（与原版直接读 process.memoryUsage() 对齐，避免 2s 采样延迟）。
func (s *Sampler) LiveMemory() (rss, heapUsed, heapTotal uint64) {
	_, rss = sampleProcess()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	if rss == 0 {
		rss = m.Sys
	}
	return rss, m.HeapAlloc, m.HeapSys
}

// CPUCount 返回逻辑 CPU 核心数（至少 1）。
func (s *Sampler) CPUCount() int { return s.cpuCount }

// SystemTotalMem 返回系统物理内存总量（字节）；取不到为 0。
func (s *Sampler) SystemTotalMem() uint64 { return s.sysTotal }

// Stop 停止后台采样。可多次调用。
func (s *Sampler) Stop() { s.stopOnce.Do(func() { close(s.stopCh) }) }
