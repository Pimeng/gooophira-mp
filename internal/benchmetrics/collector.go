package benchmetrics

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ── Collector ──────────────────────────────────────────────────────────
//
// Collector 是零分配、锁友好的压测指标采集器。
// 所有热路径计数器使用 atomic 操作；直方图使用 per-shard Mutex（~1ns 持锁）。
// 适用于毫秒级基准测试，不会成为吞吐瓶颈。

// Collector 聚合所有压测指标。
type Collector struct {
	// 直方图（每个样本持 ~1ns 锁，对数桶，零分配）
	cmdLatency     *Histogram
	connectLatency *Histogram
	authLatency    *Histogram

	// 命令/周期吞吐量（atomic）
	commandsSent    atomic.Int64
	cyclesCompleted atomic.Int64

	// 数据吞吐量（atomic）
	bytesIn    atomic.Int64
	bytesOut   atomic.Int64
	packetsIn  atomic.Int64
	packetsOut atomic.Int64

	// 连接统计（atomic）
	connAttempts atomic.Int64
	connSuccess  atomic.Int64
	connFailed   atomic.Int64
	authSuccess  atomic.Int64
	authFailed   atomic.Int64
	retryCount   atomic.Int64
	reconnCount  atomic.Int64
	activeConns  atomic.Int64
	peakConns    atomic.Int64

	// 每秒钟吞吐量采样
	timelineBase int64       // Unix 秒，timeline[0] 对应的时间
	timeline     [3600]int64 // 每秒钟累计命令数（最多 1 小时）
	timelineMu   sync.Mutex  // 仅 TimelineTick 调用时持有（每秒 1 次）

	// 运行时采样（atomic）
	peakGoroutines atomic.Int64
	peakHeap       atomic.Uint64

	// 误差统计（错误路径，无锁争用）
	errMu      sync.Mutex
	errCounts  map[string]int64
	errSamples []string

	// 场景计数（atomic）
	roomCreate  atomic.Int64
	roomDestroy atomic.Int64
	peakRooms   atomic.Int64
	joinSuccess atomic.Int64
	joinFailed  atomic.Int64
	leaveCount  atomic.Int64

	// 初始快照（用于 alloc rate 计算）
	initMem runtime.MemStats

	// 系统信息（只读，初始化时设置）
	numCPU     int
	goMaxProcs int

	initOnce sync.Once
	tl       *Timeline
}

// NewCollector 创建新的采集器并记录初始状态。
func NewCollector() *Collector {
	c := &Collector{
		cmdLatency:     &Histogram{},
		connectLatency: &Histogram{},
		authLatency:    &Histogram{},
		errCounts:      make(map[string]int64),
		numCPU:         runtime.NumCPU(),
		goMaxProcs:     runtime.GOMAXPROCS(0),
		errSamples:     make([]string, 0, 16),
	}

	c.tl = NewTimeline()
	c.tl.Register("cmd/s", c.commandsSent.Load, false)
	c.tl.Register("goroutines", func() int64 { return int64(runtime.NumGoroutine()) }, true)

	runtime.ReadMemStats(&c.initMem)
	c.timelineBase = time.Now().Unix()
	return c
}

// ── 延迟 ──────────────────────────────────────────────────────────────

// RecordCmdLatency 记录一条命令延迟。
func (c *Collector) RecordCmdLatency(d time.Duration) {
	c.cmdLatency.Record(d)
}

// RecordConnectLatency 记录一次 TCP 连接延迟。
func (c *Collector) RecordConnectLatency(d time.Duration) {
	c.connectLatency.Record(d)
}

// RecordAuthLatency 记录一次认证延迟。
func (c *Collector) RecordAuthLatency(d time.Duration) {
	c.authLatency.Record(d)
}

// ── 吞吐量 ────────────────────────────────────────────────────────────

// AddCommands 原子增加命令计数。
func (c *Collector) AddCommands(n int64) {
	c.commandsSent.Add(n)
}

// AddCycle 原子增加周期计数。
func (c *Collector) AddCycle() {
	c.cyclesCompleted.Add(1)
}

// AddBytesIn 原子增加输入字节。
func (c *Collector) AddBytesIn(n int64) {
	c.bytesIn.Add(n)
}

// AddBytesOut 原子增加输出字节。
func (c *Collector) AddBytesOut(n int64) {
	c.bytesOut.Add(n)
}

// AddPacketIn 原子增加输入包计数。
func (c *Collector) AddPacketIn() {
	c.packetsIn.Add(1)
}

// AddPacketOut 原子增加输出包计数。
func (c *Collector) AddPacketOut() {
	c.packetsOut.Add(1)
}

// ── 连接 ──────────────────────────────────────────────────────────────

// AddConnAttempt 记录一次连接尝试。
func (c *Collector) AddConnAttempt() {
	c.connAttempts.Add(1)
}

// AddConnSuccess 记录一次连接成功。
func (c *Collector) AddConnSuccess() {
	c.connSuccess.Add(1)
	c.activeConns.Add(1)
	for {
		cur := c.peakConns.Load()
		act := c.activeConns.Load()
		if act <= cur {
			break
		}
		if c.peakConns.CompareAndSwap(cur, act) {
			break
		}
	}
}

// AddConnFailed 记录一次连接失败。
func (c *Collector) AddConnFailed() {
	c.connFailed.Add(1)
}

// AddAuthSuccess 记录一次认证成功。
func (c *Collector) AddAuthSuccess() {
	c.authSuccess.Add(1)
}

// AddAuthFailed 记录一次认证失败。
func (c *Collector) AddAuthFailed() {
	c.authFailed.Add(1)
}

// AddRetry 记录一次重试。
func (c *Collector) AddRetry() {
	c.retryCount.Add(1)
}

// AddReconnect 记录一次重连。
func (c *Collector) AddReconnect() {
	c.reconnCount.Add(1)
}

// ConnClose 连接关闭时调用。
func (c *Collector) ConnClose() {
	c.activeConns.Add(-1)
}

// ── 采样 ──────────────────────────────────────────────────────────────

// Sample 采集运行时峰值指标（goroutine、heap），由外部 ticker 驱动。
func (c *Collector) Sample() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// 峰值堆
	for {
		old := c.peakHeap.Load()
		if m.HeapAlloc <= old {
			break
		}
		if c.peakHeap.CompareAndSwap(old, m.HeapAlloc) {
			break
		}
	}

	// 峰值 goroutine
	g := int64(runtime.NumGoroutine())
	for {
		old := c.peakGoroutines.Load()
		if g <= old {
			break
		}
		if c.peakGoroutines.CompareAndSwap(old, g) {
			break
		}
	}
}

// TimelineTick 记录当前秒的累计命令数（每秒调用 1 次）。
func (c *Collector) TimelineTick() {
	// Legacy per-second command snapshot
	now := time.Now().Unix()
	idx := int(now - c.timelineBase)
	if idx >= 0 && idx < len(c.timeline) {
		c.timelineMu.Lock()
		c.timeline[idx] = c.commandsSent.Load()
		c.timelineMu.Unlock()
	}
	// New generic timeline
	if c.tl != nil {
		c.tl.Tick()
	}
}

// errorType 从 error 中提取类型关键词。
func errorType(err error) string {
	msg := err.Error()
	// 常见的 Go 网络错误模式
	switch {
	case strings.Contains(msg, "EOF"):
		return "EOF"
	case strings.Contains(msg, "timeout"):
		return "Timeout"
	case strings.Contains(msg, "time out"):
		return "Timeout"
	case strings.Contains(msg, "connection refused"):
		return "ConnRefused"
	case strings.Contains(msg, "connection reset"):
		return "ConnReset"
	case strings.Contains(msg, "broken pipe"):
		return "BrokenPipe"
	case strings.Contains(msg, "i/o timeout"):
		return "IOTimeout"
	case strings.Contains(msg, "no such host"):
		return "DNSFail"
	case strings.Contains(msg, "closed network connection"):
		return "ConnClosed"
	case strings.Contains(msg, "too many open files"):
		return "TooManyFD"
	case strings.Contains(msg, "cannot assign requested address"):
		return "PortExhausted"
	case strings.Contains(msg, "address already in use"):
		return "AddrInUse"
	case strings.Contains(msg, "Invalid frame"):
		return "InvalidFrame"
	case strings.Contains(msg, "checksum"):
		return "ChecksumErr"
	case strings.Contains(msg, "protocol"):
		return "ProtocolErr"
	case strings.Contains(msg, "authenticate"):
		return "AuthFail"
	default:
		return fmt.Sprintf("Other(%s)", msg[:min(len(msg), 40)])
	}
}

// RecordError 记录一个错误（分类计数 + 样本去重）。
func (c *Collector) RecordError(err error) {
	if err == nil {
		return
	}
	tp := errorType(err)

	c.errMu.Lock()
	c.errCounts[tp]++
	if len(c.errSamples) < 16 {
		msg := err.Error()
		found := false
		for _, s := range c.errSamples {
			if s == msg {
				found = true
				break
			}
		}
		if !found {
			c.errSamples = append(c.errSamples, msg)
		}
	}
	c.errMu.Unlock()
}

// ── 场景 ──────────────────────────────────────────────────────────────

// AddRoomCreate 记录一次房间创建。
func (c *Collector) AddRoomCreate() { c.roomCreate.Add(1) }

// AddRoomDestroy 记录一次房间销毁。
func (c *Collector) AddRoomDestroy() { c.roomDestroy.Add(1) }

// SetPeakRooms 尝试设置峰值房间数（仅在更新时成功）。
func (c *Collector) SetPeakRooms(n int64) {
	for {
		old := c.peakRooms.Load()
		if n <= old {
			break
		}
		if c.peakRooms.CompareAndSwap(old, n) {
			break
		}
	}
}

// AddJoinSuccess 记录一次加入房间成功。
func (c *Collector) AddJoinSuccess() { c.joinSuccess.Add(1) }

// AddJoinFailed 记录一次加入房间失败。
func (c *Collector) AddJoinFailed() { c.joinFailed.Add(1) }

// AddLeave 记录一次离开房间。
func (c *Collector) AddLeave() { c.leaveCount.Add(1) }

// ── 快照 ──────────────────────────────────────────────────────────────

// Snap 从采集器当前状态生成 BenchResult。
// elapsed 为实际压测耗时（从开始到结束），用于计算平均速率。
func (c *Collector) Snap(config BenchRunConfig, elapsed time.Duration) BenchResult {
	cmds := c.commandsSent.Load()
	cycles := c.cyclesCompleted.Load()

	elapsedSec := elapsed.Seconds()

	// 吞吐量
	peakCmdPerSec := c.computePeakCmdPerSec()
	ts := ThroughputStats{
		CommandsSent:      cmds,
		CyclesCompleted:   cycles,
		AvgCmdsPerSec:     float64(cmds) / elapsedSec,
		PeakCmdsPerSec:    peakCmdPerSec,
		BytesIn:           c.bytesIn.Load(),
		BytesOut:          c.bytesOut.Load(),
		BytesPerSecIn:     float64(c.bytesIn.Load()) / elapsedSec,
		BytesPerSecOut:    float64(c.bytesOut.Load()) / elapsedSec,
		PacketsIn:         c.packetsIn.Load(),
		PacketsOut:        c.packetsOut.Load(),
		PerSecondTimeline: c.computeTimeline(),
	}

	// 连接
	connAtt := c.connAttempts.Load()
	connSuc := c.connSuccess.Load()
	connFal := c.connFailed.Load()
	authSuc := c.authSuccess.Load()
	authFal := c.authFailed.Load()

	var successRate, authRate float64
	if connAtt > 0 {
		successRate = float64(connSuc) / float64(connAtt) * 100
	}
	if authSuc+authFal > 0 {
		authRate = float64(authSuc) / float64(authSuc+authFal) * 100
	}

	cs := ConnectionStats{
		Attempts:    connAtt,
		Success:     connSuc,
		Failed:      connFal,
		AuthSuccess: authSuc,
		AuthFailed:  authFal,
		RetryCount:  c.retryCount.Load(),
		ReconnCount: c.reconnCount.Load(),
		ActiveConns: c.activeConns.Load(),
		PeakConns:   c.peakConns.Load(),
		SuccessRate: successRate,
		AuthRate:    authRate,
	}

	// 错误
	es := c.computeErrors()

	// 运行时
	var finalMem runtime.MemStats
	runtime.ReadMemStats(&finalMem)

	totalGCPause := finalMem.PauseTotalNs
	avgGCPause := time.Duration(0)
	maxGCPause := time.Duration(0)
	if finalMem.NumGC > 0 {
		avgGCPause = time.Duration(totalGCPause / uint64(finalMem.NumGC))
		// 查找最大 GC 暂停
		for i := 0; i < 256; i++ {
			p := time.Duration(finalMem.PauseNs[i])
			if p > maxGCPause {
				maxGCPause = p
			}
		}
	}

	allocDelta := int64(finalMem.TotalAlloc - c.initMem.TotalAlloc)
	allocRate := float64(allocDelta) / 1024 / 1024 / elapsedSec

	rs := RuntimeStats{
		NumCPU:          c.numCPU,
		GOMAXPROCS:      c.goMaxProcs,
		PeakGoroutines:  c.peakGoroutines.Load(),
		FinalGoroutines: int64(runtime.NumGoroutine()),
		PeakHeapMB:      float64(c.peakHeap.Load()) / 1024 / 1024,
		FinalHeapMB:     float64(finalMem.HeapAlloc) / 1024 / 1024,
		HeapAllocMB:     float64(finalMem.HeapAlloc) / 1024 / 1024,
		HeapObjects:     finalMem.HeapObjects,
		StackMB:         float64(finalMem.StackInuse) / 1024 / 1024,
		SysMemMB:        float64(finalMem.Sys) / 1024 / 1024,
		NumGC:           finalMem.NumGC,
		TotalGCPause:    time.Duration(totalGCPause),
		AvgGCPause:      avgGCPause,
		MaxGCPause:      maxGCPause,
		AllocRateMB:     allocRate,
	}

	// 场景
	sc := ScenarioStats{}
	if config.Clients > 0 && config.Rooms > 0 {
		sc.RoomCycle = &RoomCycleStats{
			RoomCreateCount:   c.roomCreate.Load(),
			RoomDestroyCount:  c.roomDestroy.Load(),
			PeakRooms:         c.peakRooms.Load(),
			AvgPlayersPerRoom: float64(config.Clients) / float64(max(1, config.Rooms)),
			JoinSuccess:       c.joinSuccess.Load(),
			JoinFailed:        c.joinFailed.Load(),
			LeaveCount:        c.leaveCount.Load(),
		}
	}

	result := BenchResult{
		Config:         config,
		Duration:       elapsed,
		Throughput:     ts,
		ConnectLatency: c.connectLatency.Compute(),
		AuthLatency:    c.authLatency.Compute(),
		CmdLatency:     c.cmdLatency.Compute(),
		Connection:     cs,
		Errors:         es,
		Runtime:        rs,
		Scenario:       sc,
	}
	if c.tl != nil {
		result.TimelineSeries = c.tl.Snap()
	}
	result.Analysis = AnalyzeResult(&result)
	return result
}

// computePeakCmdPerSec 从每秒钟时间线计算峰值命令速率。
func (c *Collector) computePeakCmdPerSec() float64 {
	c.timelineMu.Lock()
	defer c.timelineMu.Unlock()

	peak := int64(0)
	prev := int64(0)
	first := true
	for _, v := range c.timeline {
		if v == 0 {
			continue
		}
		if first {
			prev = v
			first = false
			continue
		}
		delta := v - prev
		if delta > peak {
			peak = delta
		}
		prev = v
	}
	return float64(peak)
}

// computeTimeline 计算每秒钟命令速率时间线。
func (c *Collector) computeTimeline() []int64 {
	c.timelineMu.Lock()
	defer c.timelineMu.Unlock()

	// 找出非零范围
	first := -1
	last := -1
	for i, v := range c.timeline {
		if v > 0 {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 || last < first {
		return nil
	}

	timeline := make([]int64, 0, last-first+1)
	prev := c.timeline[first]
	for i := first + 1; i <= last; i++ {
		cur := c.timeline[i]
		delta := cur - prev
		timeline = append(timeline, delta)
		prev = cur
	}
	// 如果只有 1 个数据点，直接返回当前速率
	if len(timeline) == 0 {
		return nil
	}
	return timeline
}

// computeErrors 从内部 map 生成排序后的 ErrorStats。
func (c *Collector) computeErrors() ErrorStats {
	c.errMu.Lock()
	defer c.errMu.Unlock()

	total := int64(0)
	for _, v := range c.errCounts {
		total += v
	}

	if total == 0 {
		return ErrorStats{}
	}

	sorted := make([]ErrorCount, 0, len(c.errCounts))
	for tp, cnt := range c.errCounts {
		sorted = append(sorted, ErrorCount{Type: tp, Count: cnt})
	}
	// 按数量降序排序
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].Count > sorted[i].Count {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	return ErrorStats{
		Total:   total,
		ByType:  sorted,
		Samples: c.errSamples,
	}
}

// ── GC 辅助 ────────────────────────────────────────────────────────────

var initGCPercent = debug.SetGCPercent(-1) // 读取当前 GOGC

func init() {
	debug.SetGCPercent(initGCPercent) // 恢复
}

// min/max intrinsics for Go 1.21+, but we provide explicit for compatibility
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
