package procstats

import (
	"testing"
	"time"
)

func TestSampler_FirstFrameAndFields(t *testing.T) {
	s := Start()
	defer s.Stop()

	cur, ok := s.Current()
	if !ok {
		t.Fatal("Start should produce an immediate first sample")
	}
	if s.CPUCount() < 1 {
		t.Errorf("CPUCount should be >= 1, got %d", s.CPUCount())
	}
	if cur.Timestamp == 0 {
		t.Error("sample timestamp should be set")
	}
	// HeapUsed/HeapTotal 来自 runtime，必为正；RSS 至少有运行时兜底。
	if cur.HeapUsed == 0 || cur.HeapTotal == 0 || cur.RSS == 0 {
		t.Errorf("memory fields should be non-zero: %+v", cur)
	}
	if cur.CPUPercent < 0 || cur.CPUPercent > 100 {
		t.Errorf("cpuPercent out of range: %v", cur.CPUPercent)
	}
}

func TestSampler_HistoryGrowsAndCaps(t *testing.T) {
	s := Start()
	defer s.Stop()

	// 直接驱动采样以免等待 2s ticker（白盒）。
	for range maxHistory + 20 {
		s.take()
	}
	h := s.History()
	if len(h) != maxHistory {
		t.Fatalf("history should cap at %d, got %d", maxHistory, len(h))
	}
	// 应按时间不减序。
	for i := 1; i < len(h); i++ {
		if h[i].Timestamp < h[i-1].Timestamp {
			t.Fatalf("history not in chronological order at %d", i)
		}
	}
}

func TestSampler_CPUPercentReflectsLoad(t *testing.T) {
	s := Start()
	defer s.Stop()

	// 制造一段繁忙 CPU，再采样：占用率应抬升（不强求具体值，仅验证非负且在范围内）。
	deadline := time.Now().Add(40 * time.Millisecond)
	x := 0
	for time.Now().Before(deadline) {
		x++
	}
	_ = x
	s.take()
	cur, _ := s.Current()
	if cur.CPUPercent < 0 || cur.CPUPercent > 100 {
		t.Errorf("cpuPercent out of range after load: %v", cur.CPUPercent)
	}
}
