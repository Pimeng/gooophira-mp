package benchmetrics

import (
	"sync"
	"time"
)

// ── Timeline ─────────────────────────────────────────────────────────
//
// Timeline provides per-second tracking for any metric.
// Call Register() for each metric, then Tick() once per second.
// Deltas are computed automatically for cumulative counters.

const timelineMaxSlots = 3600 // 1 hour max

// TimelineSerie holds one time series of per-second values.
type TimelineSerie struct {
	Name   string  `json:"name"`
	Values []int64 `json:"values"`
}

// Timeline manages per-second snapshots of multiple metrics.
type Timeline struct {
	mu     sync.Mutex
	base   int64 // unix timestamp of slot 0
	series []*timelineSerieInternal
}

type timelineSerieInternal struct {
	name    string
	getter  func() int64
	prev    int64
	slots   [timelineMaxSlots]int64
	isGauge bool // if true, store raw value; if false, store delta
}

// NewTimeline creates a timeline with the given base timestamp.
func NewTimeline() *Timeline {
	return &Timeline{
		base:   time.Now().Unix(),
		series: make([]*timelineSerieInternal, 0, 8),
	}
}

// Register adds a metric to the timeline.
// For cumulative counters (commands sent, bytes, etc.), isGauge=false.
// For instantaneous values (goroutines, heap, etc.), isGauge=true.
func (t *Timeline) Register(name string, getter func() int64, isGauge bool) {
	t.mu.Lock()
	serie := &timelineSerieInternal{
		name:    name,
		getter:  getter,
		prev:    getter(),
		isGauge: isGauge,
	}
	t.series = append(t.series, serie)
	t.mu.Unlock()
}

// Tick records a snapshot for all registered metrics.
// Call this once per second.
func (t *Timeline) Tick() {
	idx := int(time.Now().Unix() - t.base)
	if idx < 0 || idx >= timelineMaxSlots {
		return
	}
	t.mu.Lock()
	for _, s := range t.series {
		cur := s.getter()
		var val int64
		if s.isGauge {
			val = cur
		} else {
			val = cur - s.prev
			if val < 0 {
				val = 0
			}
			s.prev = cur
		}
		s.slots[idx] = val
	}
	t.mu.Unlock()
}

// Snap returns all time series with their values.
func (t *Timeline) Snap() []TimelineSerie {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]TimelineSerie, 0, len(t.series))
	for _, s := range t.series {
		// Find the actual data range
		first := -1
		last := -1
		for i := range s.slots {
			if s.slots[i] != 0 {
				if first < 0 {
					first = i
				}
				last = i
			}
		}
		if first < 0 || last < first {
			continue
		}
		values := make([]int64, 0, last-first+1)
		for i := first; i <= last; i++ {
			values = append(values, s.slots[i])
		}
		result = append(result, TimelineSerie{Name: s.name, Values: values})
	}
	return result
}

// SetBase sets the base timestamp (for testing or initialization).
func (t *Timeline) SetBase(base int64) {
	t.mu.Lock()
	t.base = base
	t.mu.Unlock()
}
