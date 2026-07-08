package benchmetrics

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// ── Exporter ─────────────────────────────────────────────────────────
//
// Exporter converts benchmark results to different output formats.
// Currently supports: JSON, CSV
// Future: Prometheus, Grafana (via json tags ready)

// ExportFormat defines the output format.
type ExportFormat string

const (
	FormatJSON ExportFormat = "json"
	FormatCSV  ExportFormat = "csv"
)

// --- JSON ---

// ExportJSON serializes a BenchReport as indented JSON.
func ExportJSON(report *BenchReport) ([]byte, error) {
	return json.MarshalIndent(report, "", "  ")
}

// ExportJSONTo writes JSON to a writer.
func ExportJSONTo(w io.Writer, report *BenchReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

// --- CSV ---

// ExportCSV converts benchmark results to CSV format.
// Returns CSV content as a byte slice.
func ExportCSV(report *BenchReport) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)

	// Header row
	headers := []string{
		"timestamp", "scenario", "duration_s",
		"clients", "rooms", "concurrency",
		"commands_sent", "cycles_completed",
		"avg_cmd_per_sec", "peak_cmd_per_sec",
		"bytes_in", "bytes_out",
		"latency_count", "latency_min_ns", "latency_max_ns",
		"latency_mean_ns", "latency_p50_ns", "latency_p90_ns",
		"latency_p95_ns", "latency_p99_ns", "latency_p99_9_ns",
		"latency_stddev_ns",
		"conn_attempts", "conn_success", "conn_failed",
		"auth_success", "auth_failed",
		"conn_success_rate", "auth_success_rate",
		"errors_total",
		"peak_goroutines", "peak_heap_mb", "final_heap_mb",
		"num_gc", "avg_gc_pause_ns", "max_gc_pause_ns",
		"alloc_rate_mb_per_sec",
	}
	if err := w.Write(headers); err != nil {
		return nil, err
	}

	// Data rows
	ts := time.Unix(report.Timestamp, 0).Format(time.RFC3339)
	for _, r := range report.Results {
		row := []string{
			ts, r.Name, fmt.Sprintf("%.3f", r.Duration.Seconds()),
			fmt.Sprintf("%d", r.Config.Clients), fmt.Sprintf("%d", r.Config.Rooms), fmt.Sprintf("%d", r.Config.Concurrency),
			fmt.Sprintf("%d", r.Throughput.CommandsSent), fmt.Sprintf("%d", r.Throughput.CyclesCompleted),
			fmt.Sprintf("%.2f", r.Throughput.AvgCmdsPerSec), fmt.Sprintf("%.2f", r.Throughput.PeakCmdsPerSec),
			fmt.Sprintf("%d", r.Throughput.BytesIn), fmt.Sprintf("%d", r.Throughput.BytesOut),
		}
		// Use the primary latency (CmdLatency or ConnectLatency)
		lat := r.CmdLatency
		if lat.Count == 0 {
			lat = r.ConnectLatency
		}
		row = append(row,
			fmt.Sprintf("%d", lat.Count),
			fmt.Sprintf("%d", lat.Min.Nanoseconds()),
			fmt.Sprintf("%d", lat.Max.Nanoseconds()),
			fmt.Sprintf("%d", lat.Mean.Nanoseconds()),
			fmt.Sprintf("%d", lat.P50.Nanoseconds()),
			fmt.Sprintf("%d", lat.P90.Nanoseconds()),
			fmt.Sprintf("%d", lat.P95.Nanoseconds()),
			fmt.Sprintf("%d", lat.P99.Nanoseconds()),
			fmt.Sprintf("%d", lat.P999.Nanoseconds()),
			fmt.Sprintf("%d", lat.StdDev.Nanoseconds()),
		)
		row = append(row,
			fmt.Sprintf("%d", r.Connection.Attempts),
			fmt.Sprintf("%d", r.Connection.Success),
			fmt.Sprintf("%d", r.Connection.Failed),
			fmt.Sprintf("%d", r.Connection.AuthSuccess),
			fmt.Sprintf("%d", r.Connection.AuthFailed),
			fmt.Sprintf("%.1f", r.Connection.SuccessRate),
			fmt.Sprintf("%.1f", r.Connection.AuthRate),
			fmt.Sprintf("%d", r.Errors.Total),
		)
		row = append(row,
			fmt.Sprintf("%d", r.Runtime.PeakGoroutines),
			fmt.Sprintf("%.1f", r.Runtime.PeakHeapMB),
			fmt.Sprintf("%.1f", r.Runtime.FinalHeapMB),
			fmt.Sprintf("%d", r.Runtime.NumGC),
			fmt.Sprintf("%d", r.Runtime.AvgGCPause.Nanoseconds()),
			fmt.Sprintf("%d", r.Runtime.MaxGCPause.Nanoseconds()),
			fmt.Sprintf("%.1f", r.Runtime.AllocRateMB),
		)
		if err := w.Write(row); err != nil {
			return nil, err
		}
	}

	w.Flush()
	return buf.Bytes(), w.Error()
}
