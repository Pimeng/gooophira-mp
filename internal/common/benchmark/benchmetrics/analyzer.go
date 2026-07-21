package benchmetrics

import (
	"fmt"
	"strings"
)

// ── 分析器 ───────────────────────────────────────────────────────────
//
// Analyzer 自动评估基准测试结果并生成：
//   - 总体性能评分（Excellent / Good / Fair / Poor）
//   - 发现问题的警告
//   - 改进建议
//   - 人类可读的摘要

// PerformanceScore 评定基准测试的总体质量。
type PerformanceScore int

const (
	ScoreExcellent PerformanceScore = 100
	ScoreGood      PerformanceScore = 75
	ScoreFair      PerformanceScore = 50
	ScorePoor      PerformanceScore = 25
)

func (s PerformanceScore) String() string {
	switch s {
	case ScoreExcellent:
		return "Excellent"
	case ScoreGood:
		return "Good"
	case ScoreFair:
		return "Fair"
	case ScorePoor:
		return "Poor"
	default:
		return "Unknown"
	}
}

// AnalysisWarning 描述检测到的问题。
type AnalysisWarning struct {
	Severity string `json:"severity"` // 可取 "high"、"medium"、"low"。
	Message  string `json:"message"`
}

// AnalysisResult 包含完整分析输出。
type AnalysisResult struct {
	Score       PerformanceScore  `json:"score"`
	Warnings    []AnalysisWarning `json:"warnings,omitempty"`
	Suggestions []string          `json:"suggestions,omitempty"`
	Summary     string            `json:"summary"`
}

// AnalyzeResult 自动分析一份基准测试结果。
func AnalyzeResult(r *BenchResult) *AnalysisResult {
	var score int
	var warnings []AnalysisWarning
	var suggestions []string

	// ── 长尾延迟分析 ───────────────────────────────────────────────
	if r.CmdLatency.Count > 0 {
		avgMs := r.CmdLatency.Mean.Seconds() * 1000
		p99Ms := r.CmdLatency.P99.Seconds() * 1000
		p999Ms := r.CmdLatency.P999.Seconds() * 1000

		ratio := 1.0
		if avgMs > 0 {
			ratio = p99Ms / avgMs
		}

		if ratio > 10 {
			warnings = append(warnings, AnalysisWarning{
				Severity: "high",
				Message:  fmt.Sprintf("Tail latency is %.1fx the average (P99=%.2fms vs avg=%.2fms). Indicates jitter or GC spikes.", ratio, p99Ms, avgMs),
			})
			suggestions = append(suggestions, "Investigate GC pausing or lock contention causing tail latency spikes.")
		} else if ratio > 5 {
			warnings = append(warnings, AnalysisWarning{
				Severity: "medium",
				Message:  fmt.Sprintf("Tail latency is %.1fx the average (P99 %.1f ms).", ratio, p99Ms),
			})
		}

		if p999Ms > p99Ms*2 {
			warnings = append(warnings, AnalysisWarning{
				Severity: "high",
				Message:  fmt.Sprintf("P99.9 latency (%.2fms) is >2x P99 (%.2fms). Rare but extreme outliers present.", p999Ms, p99Ms),
			})
		}
	}

	// ── GC 健康状况 ───────────────────────────────────────────────
	if r.Runtime.NumGC > 0 {
		avgPauseMs := r.Runtime.AvgGCPause.Seconds() * 1000
		maxPauseMs := r.Runtime.MaxGCPause.Seconds() * 1000

		if maxPauseMs > 100 {
			warnings = append(warnings, AnalysisWarning{
				Severity: "high",
				Message:  fmt.Sprintf("Max GC pause is %.1fms (>100ms). Will cause visible latency spikes.", maxPauseMs),
			})
			suggestions = append(suggestions, "Increase GOGC or reduce allocation rate to lower GC pause times.")
		} else if maxPauseMs > 30 {
			warnings = append(warnings, AnalysisWarning{
				Severity: "medium",
				Message:  fmt.Sprintf("Max GC pause is %.1fms. May impact tail latency.", maxPauseMs),
			})
		}

		if avgPauseMs > 10 {
			warnings = append(warnings, AnalysisWarning{
				Severity: "medium",
				Message:  fmt.Sprintf("Average GC pause is %.1fms. Consider reducing allocation rate.", avgPauseMs),
			})
		}

		if r.Runtime.AllocRateMB > 100 {
			suggestions = append(suggestions, fmt.Sprintf("Allocation rate is %.0f MB/s. Object pooling or buffer reuse may improve performance.", r.Runtime.AllocRateMB))
		}
	}

	// ── 内存增长 ──────────────────────────────────────────────────
	if r.Runtime.FinalHeapMB > 0 && r.Runtime.PeakHeapMB > r.Runtime.FinalHeapMB*1.5 {
		warnings = append(warnings, AnalysisWarning{
			Severity: "low",
			Message:  fmt.Sprintf("Heap grew to %.1f MB during test but settled at %.1f MB. Possible allocation burst mid-test.", r.Runtime.PeakHeapMB, r.Runtime.FinalHeapMB),
		})
	}

	// ── 吞吐稳定性 ────────────────────────────────────────────────
	if r.Throughput.PeakCmdsPerSec > 0 && r.Throughput.AvgCmdsPerSec > 0 {
		stability := r.Throughput.PeakCmdsPerSec / r.Throughput.AvgCmdsPerSec
		if stability > 3 {
			warnings = append(warnings, AnalysisWarning{
				Severity: "medium",
				Message:  fmt.Sprintf("Throughput is unstable: peak is %.1fx the average. Rate limiting or bursty behavior detected.", stability),
			})
		}
	}

	// ── 连接质量 ─────────────────────────────────────────────────
	if r.Connection.Success+r.Connection.Failed > 0 {
		if r.Connection.SuccessRate < 99 {
			warnings = append(warnings, AnalysisWarning{
				Severity: "high",
				Message:  fmt.Sprintf("Connection success rate is %.1f%%. Below 99%% indicates server capacity or network issues.", r.Connection.SuccessRate),
			})
			suggestions = append(suggestions, "Check OS TCP backlog settings (somaxconn), increase -virtual-ips, or reduce concurrency rate.")
		}
		if r.Connection.AuthRate > 0 && r.Connection.AuthRate < 99 {
			warnings = append(warnings, AnalysisWarning{
				Severity: "high",
				Message:  fmt.Sprintf("Auth success rate is %.1f%%. Token validation or hub capacity may be overloaded.", r.Connection.AuthRate),
			})
		}
	}

	// ── 协议错误 ─────────────────────────────────────────────────
	if r.Errors.Total > 0 {
		for _, ec := range r.Errors.ByType {
			if ec.Count > 10 {
				warnings = append(warnings, AnalysisWarning{
					Severity: "medium",
					Message:  fmt.Sprintf("%d occurrences of '%s' error.", ec.Count, ec.Type),
				})
			}
		}
		if r.Errors.Total > 100 {
			suggestions = append(suggestions, fmt.Sprintf("High error count (%d). Consider increasing timeouts or reducing load.", r.Errors.Total))
		}
	}

	// ── 计算评分 ─────────────────────────────────────────────────
	score = 100
	for _, w := range warnings {
		switch w.Severity {
		case "high":
			score -= 20
		case "medium":
			score -= 10
		case "low":
			score -= 5
		}
	}
	if score <= 0 {
		score = 25
	} else if score < 50 {
		score = 50
	} else if score < 75 {
		score = 75
	}

	// ── 摘要 ─────────────────────────────────────────────────────
	var summaryParts []string
	summaryParts = append(summaryParts, fmt.Sprintf("%s performance score with %d warning(s).", PerformanceScore(score), len(warnings)))

	if r.Throughput.AvgCmdsPerSec > 0 {
		rate := formatFloat(r.Throughput.AvgCmdsPerSec, "cmd/s")
		summaryParts = append(summaryParts, fmt.Sprintf("Average throughput: %s", rate))
	}
	if r.CmdLatency.Count > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("Latency: avg=%s p99=%s", FormatDuration(r.CmdLatency.Mean), FormatDuration(r.CmdLatency.P99)))
	}
	if r.Errors.Total > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("Errors: %d", r.Errors.Total))
	}

	return &AnalysisResult{
		Score:       PerformanceScore(score),
		Warnings:    warnings,
		Suggestions: suggestions,
		Summary:     strings.Join(summaryParts, " | "),
	}
}
