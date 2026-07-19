package benchmetrics

import (
	"sync"
	"time"
)

// ── 时间线 ───────────────────────────────────────────────────────────
//
// Timeline 为任意指标提供逐秒跟踪。
// 每个指标调用一次 Register()，之后每秒调用一次 Tick()。
// 累计计数器的差值会自动计算。

const timelineMaxSlots = 3600 // 最长 1 小时

// TimelineSerie 保存一组逐秒值时间序列。
type TimelineSerie struct {
	Name   string  `json:"name"`
	Values []int64 `json:"values"`
}

// Timeline 管理多个指标的逐秒快照。
type Timeline struct {
	mu     sync.Mutex
	base   int64 // 第 0 个槽位的 Unix 时间戳
	series []*timelineSerieInternal
}

type timelineSerieInternal struct {
	name    string
	getter  func() int64
	prev    int64
	slots   [timelineMaxSlots]int64
	isGauge bool // true 保存原始值，false 保存差值
}

// NewTimeline 创建使用当前时间作为基准时间戳的时间线。
func NewTimeline() *Timeline {
	return &Timeline{
		base:   time.Now().Unix(),
		series: make([]*timelineSerieInternal, 0, 8),
	}
}

// Register 向时间线添加指标。
// 累计计数器（已发送命令、字节数等）使用 isGauge=false；
// 瞬时值（goroutine、堆等）使用 isGauge=true。
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

// Tick 记录全部已注册指标的快照，应每秒调用一次。
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

// Snap 返回全部时间序列及其值。
func (t *Timeline) Snap() []TimelineSerie {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]TimelineSerie, 0, len(t.series))
	for _, s := range t.series {
		// 查找实际数据范围。
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

// SetBase 设置基准时间戳，供测试或初始化使用。
func (t *Timeline) SetBase(base int64) {
	t.mu.Lock()
	t.base = base
	t.mu.Unlock()
}
