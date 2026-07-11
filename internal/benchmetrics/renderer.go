package benchmetrics

import (
	"fmt"
	"io"

	"strings"
	"time"
)

type Renderer struct {
	w     io.Writer
	title string
}

func NewRenderer(w io.Writer, title string) *Renderer {
	return &Renderer{w: w, title: title}
}

func (r *Renderer) Render(report *BenchReport) {
	for i, res := range report.Results {
		if i > 0 {
			fmt.Fprintln(r.w)
		}
		r.renderResult(res)
	}
	if len(report.Profiles) > 0 {
		r.renderProfiles(report.Profiles)
	}
}

func (r *Renderer) renderResult(res BenchResult) {
	r.header(res)
	r.sectionThroughput(res.Throughput)
	r.sectionLatency(res)
	r.sectionConnection(res.Connection)
	r.sectionErrors(res.Errors)
	r.sectionRuntime(res.Runtime, res.Duration)
	r.sectionTimeline(res.Throughput.PerSecondTimeline)
	r.sectionTimeBreakdown(res.TimeBreakdown)
}

func (r *Renderer) header(res BenchResult) {
	fmt.Fprintf(r.w, "══════════════════════════════════════════════════════════════════\n")
	fmt.Fprintf(r.w, "  %s\n", r.title)
	fmt.Fprintf(r.w, "══════════════════════════════════════════════════════════════════\n")
	fmt.Fprintf(r.w, "  Scenario:    %s\n", res.Name)
	fmt.Fprintf(r.w, "  Duration:    %s\n", FormatDuration(res.Duration))
	fmt.Fprintf(r.w, "  Clients:     %s    Rooms: %d    Concurrency: %d\n",
		thousandSep(int64(res.Config.Clients)), res.Config.Rooms, res.Config.Concurrency)
	fmt.Fprintln(r.w)
}

func (r *Renderer) sectionThroughput(ts ThroughputStats) {
	r.sectionTitle("Throughput")
	if ts.CommandsSent > 0 {
		fmt.Fprintf(r.w, "  Commands Sent:     %s\n", thousandSep(ts.CommandsSent))
	}
	if ts.CyclesCompleted > 0 {
		fmt.Fprintf(r.w, "  Cycles Completed:  %s\n", thousandSep(ts.CyclesCompleted))
	}
	if ts.AvgCmdsPerSec > 0 {
		fmt.Fprintf(r.w, "  Avg Throughput:    %s cmd/s\n", formatFloat(ts.AvgCmdsPerSec, ""))
	}
	if ts.PeakCmdsPerSec > 0 {
		fmt.Fprintf(r.w, "  Peak Throughput:   %s cmd/s\n", formatFloat(ts.PeakCmdsPerSec, ""))
	}
	if ts.BytesIn > 0 || ts.BytesOut > 0 {
		bIn, uIn := scaleBytes(ts.BytesIn)
		bOut, uOut := scaleBytes(ts.BytesOut)
		bsIn, _ := scaleBytes(int64(ts.BytesPerSecIn))
		bsOut, _ := scaleBytes(int64(ts.BytesPerSecOut))
		fmt.Fprintf(r.w, "  Data In:           %.2f %s  (%.2f %s/s)\n", bIn, uIn, bsIn, uIn)
		fmt.Fprintf(r.w, "  Data Out:          %.2f %s  (%.2f %s/s)\n", bOut, uOut, bsOut, uOut)
	}
	if ts.PacketsIn > 0 || ts.PacketsOut > 0 {
		fmt.Fprintf(r.w, "  Packets:           %s in / %s out\n",
			thousandSep(ts.PacketsIn), thousandSep(ts.PacketsOut))
	}
	fmt.Fprintln(r.w)
}

func (r *Renderer) sectionLatency(res BenchResult) {
	r.sectionTitle("Latency")
	fmt.Fprintf(r.w, "  %-16s  %9s  %10s  %10s  %10s  %10s\n",
		"", "Samples", "Mean", "Min", "Max", "StdDev")
	fmt.Fprintf(r.w, "  %s\n", strings.Repeat("\u2500", 70))

	r.latencyRow("Connect", res.ConnectLatency)
	r.latencyRow("Auth", res.AuthLatency)
	r.latencyRow("Command", res.CmdLatency)

	primary := res.CmdLatency
	if res.ConnectLatency.Count > primary.Count {
		primary = res.ConnectLatency
	}
	if primary.Count == 0 {
		fmt.Fprintln(r.w)
		return
	}

	fmt.Fprintln(r.w)
	fmt.Fprintf(r.w, "  Latency Distribution (%d samples):\n", primary.Count)
	fmt.Fprintf(r.w, "  %5s  %10s  %10s  %10s  %10s\n",
		"p50", "p90", "p95", "p99", "p99.9")
	fmt.Fprintf(r.w, "  %s\n", strings.Repeat("\u2500", 55))

	for _, v := range []time.Duration{primary.P50, primary.P90, primary.P95, primary.P99, primary.P999} {
		v2, u := scaleDuration(v)
		switch u {
		case "s":
			fmt.Fprintf(r.w, "  %9.2f%s", v2, u)
		case "ms":
			fmt.Fprintf(r.w, "  %9.2f%s", v2, u)
		default:
			fmt.Fprintf(r.w, "  %9.1f%s", v2, u)
		}
	}
	fmt.Fprintln(r.w)
	fmt.Fprintln(r.w)

	if len(primary.Buckets) > 0 {
		fmt.Fprintf(r.w, "  %-20s  %12s  %8s\n", "Range", "Count", "Percent")
		fmt.Fprintf(r.w, "  %s\n", strings.Repeat("\u2500", 44))
		for _, b := range primary.Buckets {
			if b.Count > 0 {
				fmt.Fprintf(r.w, "  %-20s  %12s  %7.1f%%\n",
					b.Label, thousandSep(b.Count), b.Pct)
			}
		}
		fmt.Fprintln(r.w)
	}
}

func (r *Renderer) latencyRow(label string, ls LatencyStats) {
	if ls.Count == 0 {
		return
	}
	fmt.Fprintf(r.w, "  %-16s  %9s  %10s  %10s  %10s  %10s\n",
		label,
		thousandSep(int64(ls.Count)),
		FormatDuration(ls.Mean),
		FormatDuration(ls.Min),
		FormatDuration(ls.Max),
		FormatDuration(ls.StdDev))
}

func (r *Renderer) sectionConnection(cs ConnectionStats) {
	if cs.Attempts == 0 && cs.Success == 0 {
		return
	}
	r.sectionTitle("Connection")
	total := cs.Success + cs.Failed
	authTotal := cs.AuthSuccess + cs.AuthFailed
	r.statLine("Attempts", cs.Attempts, total)
	r.statLine("Success", cs.Success, total)
	r.statLine("Failed", cs.Failed, total)
	if authTotal > 0 {
		r.statLine("Auth Success", cs.AuthSuccess, authTotal)
		r.statLine("Auth Failed", cs.AuthFailed, authTotal)
	}
	if cs.RetryCount > 0 {
		fmt.Fprintf(r.w, "  Retries:           %s\n", thousandSep(cs.RetryCount))
	}
	if cs.ReconnCount > 0 {
		fmt.Fprintf(r.w, "  Reconnects:        %s\n", thousandSep(cs.ReconnCount))
	}
	fmt.Fprintf(r.w, "  Active Connections: %s  (peak %s)\n",
		thousandSep(cs.ActiveConns), thousandSep(cs.PeakConns))
	fmt.Fprintln(r.w)
}

func (r *Renderer) statLine(label string, val, total int64) {
	pct := 0.0
	if total > 0 {
		pct = float64(val) / float64(total) * 100
	}
	fmt.Fprintf(r.w, "  %-18s  %12s  (%5.1f%%)\n",
		label, thousandSep(val), pct)
}

func (r *Renderer) sectionErrors(es ErrorStats) {
	if es.Total == 0 {
		return
	}
	r.sectionTitle("Errors")
	fmt.Fprintf(r.w, "  Total:  %s\n", thousandSep(es.Total))
	for _, ec := range es.ByType {
		fmt.Fprintf(r.w, "    %-20s  %s\n", ec.Type, thousandSep(ec.Count))
	}
	if len(es.Samples) > 0 {
		fmt.Fprintln(r.w, "  Samples:")
		for _, s := range es.Samples {
			if len(s) > 80 {
				s = s[:80] + "..."
			}
			fmt.Fprintf(r.w, "    - %s\n", s)
		}
	}
	fmt.Fprintln(r.w)
}

func (r *Renderer) sectionRuntime(rs RuntimeStats, dur time.Duration) {
	r.sectionTitle("Runtime")
	fmt.Fprintf(r.w, "  CPU:               %d cores (GOMAXPROCS=%d)\n", rs.NumCPU, rs.GOMAXPROCS)
	fmt.Fprintf(r.w, "  Goroutines:        %s peak / %s final\n",
		thousandSep(rs.PeakGoroutines), thousandSep(rs.FinalGoroutines))
	if rs.PeakHeapMB > 0 || rs.FinalHeapMB > 0 {
		fmt.Fprintf(r.w, "  Heap:              %.1f MB peak / %.1f MB final\n",
			rs.PeakHeapMB, rs.FinalHeapMB)
	}
	if rs.HeapObjects > 0 {
		fmt.Fprintf(r.w, "  Heap Objects:      %s\n", thousandSep(int64(rs.HeapObjects)))
	}
	if rs.StackMB > 0 {
		fmt.Fprintf(r.w, "  Stack Inuse:       %.1f MB\n", rs.StackMB)
	}
	if rs.SysMemMB > 0 {
		fmt.Fprintf(r.w, "  Sys Memory:        %.1f MB\n", rs.SysMemMB)
	}
	if rs.NumGC > 0 {
		fmt.Fprintln(r.w, "  GC Stats:")
		fmt.Fprintf(r.w, "    Cycles:          %d\n", rs.NumGC)
		fmt.Fprintf(r.w, "    Total Pause:     %s\n", FormatDuration(rs.TotalGCPause))
		fmt.Fprintf(r.w, "    Avg Pause:       %s\n", FormatDuration(rs.AvgGCPause))
		fmt.Fprintf(r.w, "    Max Pause:       %s\n", FormatDuration(rs.MaxGCPause))
		if rs.AllocRateMB > 0 {
			fmt.Fprintf(r.w, "    Alloc Rate:      %.1f MB/s\n", rs.AllocRateMB)
		}
	}
	fmt.Fprintln(r.w)
}

func (r *Renderer) sectionTimeline(timeline []int64) {
	if len(timeline) == 0 {
		return
	}
	r.sectionTitle("Timeline")
	var peak int64
	for _, v := range timeline {
		if v > peak {
			peak = v
		}
	}
	fmt.Fprintf(r.w, "  Peak:  %s cmd/s\n", thousandSep(peak))
	n := len(timeline)
	if n > 60 {
		n = 60
	}
	fmt.Fprintf(r.w, "  Sec:   ")
	for i := 0; i < n; i++ {
		fmt.Fprintf(r.w, " %5d", i+1)
	}
	fmt.Fprintln(r.w)
	fmt.Fprintf(r.w, "  cmd/s: ")
	for i := 0; i < n; i++ {
		val := float64(timeline[i]) / 1e3
		if val >= 100 {
			fmt.Fprintf(r.w, " %5.0fK", val)
		} else {
			fmt.Fprintf(r.w, " %5.0f ", val)
		}
	}
	fmt.Fprintln(r.w)
	fmt.Fprintln(r.w)
}

func (r *Renderer) sectionTimeBreakdown(tb TimeBreakdown) {
	if tb.Total == 0 {
		return
	}
	r.sectionTitle("Time Breakdown")
	fmt.Fprintf(r.w, "  %-12s  %12s  %8s\n", "Phase", "Duration", "Percent")
	fmt.Fprintf(r.w, "  %s\n", strings.Repeat("\u2500", 36))
	totalSec := tb.Total.Seconds()
	r.timeRow("Startup", tb.Startup, totalSec)
	r.timeRow("Warmup", tb.Warmup, totalSec)
	r.timeRow("Benchmark", tb.Benchmark, totalSec)
	r.timeRow("Shutdown", tb.Shutdown, totalSec)
	r.timeRow("Total", tb.Total, totalSec)
	fmt.Fprintln(r.w)
}

func (r *Renderer) timeRow(label string, dur time.Duration, totalSec float64) {
	pct := 0.0
	if totalSec > 0 {
		pct = dur.Seconds() / totalSec * 100
	}
	fmt.Fprintf(r.w, "  %-12s  %12s  %7.1f%%\n", label, FormatDuration(dur), pct)
}

func (r *Renderer) renderProfiles(profiles []string) {
	r.sectionTitle("Profiling")
	fmt.Fprintln(r.w, "  Profiles Generated:")
	for _, p := range profiles {
		fmt.Fprintf(r.w, "    %s\n", p)
	}
	fmt.Fprintln(r.w)
}

func (r *Renderer) sectionTitle(title string) {
	fmt.Fprintf(r.w, "-- %s %s\n", title, strings.Repeat("-", 60-len(title)))
}
