// Package benchmetrics 提供一套高性能、可扩展的基准测试指标采集、聚合与输出框架。
//
// 架构分层：
//
//	Collector   — 零分配采集层（原子变量 + 流式直方图）
//	Renderer    — CLI 终端输出（wrk/k6 风格，自动单位换算，千分位对齐）
//	Exporter    — 结构化导出（JSON / Prometheus / Grafana），当前仅 JSON
//
// 所有统计类型带 json tag，可直接序列化。
package benchmetrics

import (
	"fmt"
	"math"
	"time"
)

// ── Latency ────────────────────────────────────────────────────────────

// LatencyStats 是延迟统计的最终计算结果，包含完整的分位值、分布和直方图。
type LatencyStats struct {
	Count   int           `json:"count"`
	Min     time.Duration `json:"min"`
	Max     time.Duration `json:"max"`
	Mean    time.Duration `json:"mean"`
	P50     time.Duration `json:"p50"`
	P90     time.Duration `json:"p90"`
	P95     time.Duration `json:"p95"`
	P99     time.Duration `json:"p99"`
	P999    time.Duration `json:"p99_9"`
	StdDev  time.Duration `json:"stddev"`
	Zero    int64         `json:"zero_count"`
	Buckets []HistoBucket `json:"buckets"`
}

// HistoBucket 直方图单个桶。
type HistoBucket struct {
	Label string  `json:"label"`
	Count int64   `json:"count"`
	Pct   float64 `json:"pct"`
}

// ── Throughput ─────────────────────────────────────────────────────────

// ThroughputStats 吞吐量统计。
type ThroughputStats struct {
	CommandsSent      int64   `json:"commands_sent"`
	CyclesCompleted   int64   `json:"cycles_completed"`
	AvgCmdsPerSec     float64 `json:"avg_cmds_per_sec"`
	PeakCmdsPerSec    float64 `json:"peak_cmds_per_sec"`
	BytesIn           int64   `json:"bytes_in"`
	BytesOut          int64   `json:"bytes_out"`
	BytesPerSecIn     float64 `json:"bytes_per_sec_in"`
	BytesPerSecOut    float64 `json:"bytes_per_sec_out"`
	PacketsIn         int64   `json:"packets_in"`
	PacketsOut        int64   `json:"packets_out"`
	PerSecondTimeline []int64 `json:"per_second_timeline,omitempty"`
}

// ── Connection ─────────────────────────────────────────────────────────

// ConnectionStats 连接生命周期统计。
type ConnectionStats struct {
	Attempts    int64   `json:"attempts"`
	Success     int64   `json:"success"`
	Failed      int64   `json:"failed"`
	AuthSuccess int64   `json:"auth_success"`
	AuthFailed  int64   `json:"auth_failed"`
	RetryCount  int64   `json:"retry_count"`
	ReconnCount int64   `json:"reconnect_count"`
	ActiveConns int64   `json:"active_connections"`
	PeakConns   int64   `json:"peak_connections"`
	SuccessRate float64 `json:"success_rate"`
	AuthRate    float64 `json:"auth_success_rate"`
}

// ── Error ──────────────────────────────────────────────────────────────

// ErrorStats 错误分类统计。
type ErrorStats struct {
	Total   int64        `json:"total"`
	ByType  []ErrorCount `json:"by_type"`
	Samples []string     `json:"samples,omitempty"`
}

// ErrorCount 单类错误计数。
type ErrorCount struct {
	Type  string `json:"type"`
	Count int64  `json:"count"`
}

// ── Runtime ────────────────────────────────────────────────────────────

// RuntimeStats 运行时资源统计。
type RuntimeStats struct {
	NumCPU          int           `json:"num_cpu"`
	GOMAXPROCS      int           `json:"gomaxprocs"`
	PeakGoroutines  int64         `json:"peak_goroutines"`
	FinalGoroutines int64         `json:"final_goroutines"`
	PeakHeapMB      float64       `json:"peak_heap_mb"`
	FinalHeapMB     float64       `json:"final_heap_mb"`
	HeapAllocMB     float64       `json:"heap_alloc_mb"`
	HeapObjects     uint64        `json:"heap_objects"`
	StackMB         float64       `json:"stack_mb"`
	SysMemMB        float64       `json:"sys_mem_mb"`
	NumGC           uint32        `json:"num_gc"`
	TotalGCPause    time.Duration `json:"total_gc_pause"`
	AvgGCPause      time.Duration `json:"avg_gc_pause"`
	MaxGCPause      time.Duration `json:"max_gc_pause"`
	AllocRateMB     float64       `json:"alloc_rate_mb_per_sec"`
}

// ── Scenario ───────────────────────────────────────────────────────────

// ScenarioStats 场景专属指标。
type ScenarioStats struct {
	RoomCycle *RoomCycleStats `json:"room_cycle,omitempty"`
}

// RoomCycleStats 房间生命周期场景专属统计。
type RoomCycleStats struct {
	RoomCreateCount   int64   `json:"room_create_count"`
	RoomDestroyCount  int64   `json:"room_destroy_count"`
	PeakRooms         int64   `json:"peak_rooms"`
	AvgPlayersPerRoom float64 `json:"avg_players_per_room"`
	JoinSuccess       int64   `json:"join_success"`
	JoinFailed        int64   `json:"join_failed"`
	LeaveCount        int64   `json:"leave_count"`
}

// ── Time Breakdown ─────────────────────────────────────────────────────

// TimeBreakdown 压测各阶段耗时及占比。
type TimeBreakdown struct {
	Startup   time.Duration `json:"startup"`
	Warmup    time.Duration `json:"warmup"`
	Benchmark time.Duration `json:"benchmark"`
	Shutdown  time.Duration `json:"shutdown"`
	Total     time.Duration `json:"total"`
}

// ── Result & Report ────────────────────────────────────────────────────

// BenchResult 单场景压测的完整结果。
type BenchResult struct {
	TimelineSeries []TimelineSerie `json:"timeline,omitempty"`
	Analysis       *AnalysisResult `json:"analysis,omitempty"`
	Name           string          `json:"name"`
	Duration       time.Duration   `json:"duration"`
	Config         BenchRunConfig  `json:"config"`
	Throughput     ThroughputStats `json:"throughput"`
	ConnectLatency LatencyStats    `json:"connect_latency,omitempty"`
	AuthLatency    LatencyStats    `json:"auth_latency,omitempty"`
	CmdLatency     LatencyStats    `json:"cmd_latency"`
	Connection     ConnectionStats `json:"connection"`
	Errors         ErrorStats      `json:"errors"`
	Runtime        RuntimeStats    `json:"runtime"`
	Scenario       ScenarioStats   `json:"scenario,omitempty"`
	TimeBreakdown  TimeBreakdown   `json:"time_breakdown"`
}

// BenchRunConfig 单次压测运行参数。
type BenchRunConfig struct {
	Clients     int           `json:"clients"`
	Rooms       int           `json:"rooms"`
	Duration    time.Duration `json:"duration"`
	Concurrency int           `json:"concurrency"`
}

// BenchReport 完整压测报告。
type BenchReport struct {
	Title     string        `json:"title"`
	Timestamp int64         `json:"timestamp"`
	Results   []BenchResult `json:"results"`
	Profiles  []string      `json:"profiles,omitempty"`
}

// ── 单位换算辅助 ──────────────────────────────────────────────────────

// scaleDuration 返回最合适的显示单位及换算值。
func scaleDuration(d time.Duration) (value float64, unit string) {
	ns := d.Nanoseconds()
	switch {
	case ns >= 1_000_000_000:
		return float64(ns) / 1e9, "s"
	case ns >= 1_000_000:
		return float64(ns) / 1e6, "ms"
	case ns >= 1_000:
		return float64(ns) / 1e3, "µs"
	default:
		return float64(ns), "ns"
	}
}

// scaleBytes 返回最合适的字节单位及换算值。
func scaleBytes(b int64) (value float64, unit string) {
	abs := b
	if abs < 0 {
		abs = -abs
	}
	switch {
	case abs >= 1<<30:
		return float64(b) / (1 << 30), "GB"
	case abs >= 1<<20:
		return float64(b) / (1 << 20), "MB"
	case abs >= 1<<10:
		return float64(b) / (1 << 10), "KB"
	default:
		return float64(b), "B"
	}
}

// thousandSep 用逗号分隔千分位。
func thousandSep(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [32]byte
	pos := len(buf)
	group := 0
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
		group++
		if group%3 == 0 && n > 0 {
			pos--
			buf[pos] = ','
		}
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// FormatDuration 格式化持续时间为最友好的字符串（自动单位）。
func FormatDuration(d time.Duration) string {
	if d == 0 {
		return "0"
	}
	v, u := scaleDuration(d)
	switch u {
	case "s":
		return fmt.Sprintf("%.2f%s", v, u)
	case "ms":
		return fmt.Sprintf("%.2f%s", v, u)
	case "µs":
		return fmt.Sprintf("%.1f%s", v, u)
	default:
		return fmt.Sprintf("%.0f%s", v, u)
	}
}

// formatPercentile 格式化百分位标签，如 "p50", "p99.9"。
func formatPercentile(p float64) string {
	if p == math.Trunc(p) {
		return fmt.Sprintf("p%.0f", p)
	}
	s := fmt.Sprintf("%.1f", p)
	for len(s) > 0 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if len(s) > 0 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return "p" + s
}

// formatFloat 保留 1 位小数并返回带单位的字符串。
func formatFloat(v float64, unit string) string {
	return fmt.Sprintf("%.1f%s", v, unit)
}
