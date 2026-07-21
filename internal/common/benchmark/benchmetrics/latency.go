package benchmetrics

import (
	"math"
	"sort"
	"sync"
	"time"
)

// pair 在内部用于直方图分桶。
type pair struct {
	idx   int
	count int64
}

// ── 对数直方图 ───────────────────────────────────────────────────────
//
// 使用 128 个对数刻度桶覆盖从 1ns 到数百年范围，桶 i 覆盖 [2^(i-1), 2^i) ns。
// 相比固定边界桶，对数桶自动适配任意数据分布，在低延迟区域提供更细粒度。

const logBucketBits = 7                  // 128 个桶
const numLogBuckets = 1 << logBucketBits // 128

// Histogram 是固定内存的流式延迟直方图（无分配），支持对数桶和百分位插值。
type Histogram struct {
	mu      sync.Mutex
	count   int64
	min     time.Duration
	max     time.Duration
	sum     int64   // 纳秒累加
	sumSq   float64 // 纳秒²累加
	zero    int64
	buckets [numLogBuckets]int64
}

// Record 记录一个延迟样本。
func (h *Histogram) Record(d time.Duration) {
	ns := d.Nanoseconds()
	h.mu.Lock()
	h.count++
	h.sum += ns
	h.sumSq += float64(ns) * float64(ns)
	if h.count == 1 || d < h.min {
		h.min = d
	}
	if d > h.max {
		h.max = d
	}
	if ns <= 0 {
		h.zero++
		h.buckets[0]++
	} else {
		idx := fls64(uint64(ns))
		if idx >= numLogBuckets {
			idx = numLogBuckets - 1
		}
		h.buckets[idx]++
	}
	h.mu.Unlock()
}

// fls64 返回最高位位置（1-based），用于确定桶索引。
// 例如: fls64(1) = 1, fls64(2) = 2, fls64(3) = 2, fls64(4) = 3
func fls64(x uint64) int {
	if x == 0 {
		return 0
	}
	return 64 - nlz64(x)
}

// nlz64 返回前导零个数。
func nlz64(x uint64) int {
	n := 64
	y := x >> 32
	if y != 0 {
		n -= 32
		x = y
	}
	y = x >> 16
	if y != 0 {
		n -= 16
		x = y
	}
	y = x >> 8
	if y != 0 {
		n -= 8
		x = y
	}
	y = x >> 4
	if y != 0 {
		n -= 4
		x = y
	}
	y = x >> 2
	if y != 0 {
		n -= 2
		x = y
	}
	y = x >> 1
	if y != 0 {
		n -= 1
	}
	return n
}

// Compute 从采集的直方图计算 LatencyStats。
func (h *Histogram) Compute() LatencyStats {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.count == 0 {
		return LatencyStats{}
	}

	n := h.count
	// 平均值
	mean := time.Duration(h.sum / n)

	// 标准差
	variance := h.sumSq/float64(n) - float64(h.sum)*float64(h.sum)/float64(n)/float64(n)
	if variance < 0 {
		variance = 0
	}
	stdDev := time.Duration(math.Sqrt(variance))

	// 百分位（桶内插值）
	p50 := h.percentile(0.50)
	p90 := h.percentile(0.90)
	p95 := h.percentile(0.95)
	p99 := h.percentile(0.99)
	p999 := h.percentile(0.999)

	// 自适应直方图（合并相邻空桶，仅显示非零区间）
	buckets := h.buildBucketsLocked()

	return LatencyStats{
		Count:   int(n),
		Min:     h.min,
		Max:     h.max,
		Mean:    mean,
		P50:     p50,
		P90:     p90,
		P95:     p95,
		P99:     p99,
		P999:    p999,
		StdDev:  stdDev,
		Zero:    h.zero,
		Buckets: buckets,
	}
}

// percentile 计算指定百分位值，使用桶内线性插值提高精度。
func (h *Histogram) percentile(p float64) time.Duration {
	target := int64(float64(h.count) * p)
	if target <= 0 {
		return h.min
	}
	if target >= h.count {
		return h.max
	}

	var cum int64
	// 先处理桶 0（0ns）
	if h.buckets[0] > 0 {
		cum += h.buckets[0]
		if cum >= target {
			return 0
		}
	}

	for i := 1; i < numLogBuckets; i++ {
		c := h.buckets[i]
		if c == 0 {
			continue
		}
		prevCum := cum
		cum += c
		if cum >= target {
			// 桶 i 覆盖 [2^(i-1), 2^i) ns
			low := int64(1 << (uint(i) - 1))
			high := int64(1 << uint(i))
			frac := float64(target-prevCum) / float64(c)
			val := low + int64(frac*float64(high-low))
			return time.Duration(val)
		}
	}
	return h.max
}

// buildBucketsLocked 构建用于展示的直方图桶列表（已持有锁）。
func (h *Histogram) buildBucketsLocked() []HistoBucket {
	total := float64(h.count)
	if total == 0 {
		return nil
	}

	// 收集非零桶

	var nonZero []pair
	for i := range numLogBuckets {
		if h.buckets[i] > 0 {
			nonZero = append(nonZero, pair{idx: i, count: h.buckets[i]})
		}
	}

	if len(nonZero) == 0 {
		return nil
	}

	// 合并相邻桶以减少输出行数（最长显示 12 行）
	const maxDisplay = 12
	if len(nonZero) <= maxDisplay {
		return h.renderBuckets(nonZero, total)
	}

	// 从首尾分别向中间合并
	for len(nonZero) > maxDisplay {
		// 找到最小合并代价的相邻对
		bestCost := int64(1<<63 - 1)
		bestIdx := 0
		for i := 0; i < len(nonZero)-1; i++ {
			cost := nonZero[i].count + nonZero[i+1].count
			if cost < bestCost {
				bestCost = cost
				bestIdx = i
			}
		}
		// 合并
		merged := pair{
			idx:   nonZero[bestIdx].idx,
			count: nonZero[bestIdx].count + nonZero[bestIdx+1].count,
		}
		nonZero = append(nonZero[:bestIdx], nonZero[bestIdx+1:]...)
		nonZero[bestIdx] = merged
	}

	return h.renderBuckets(nonZero, total)
}

func (h *Histogram) renderBuckets(buckets []pair, total float64) []HistoBucket {
	out := make([]HistoBucket, 0, len(buckets))
	for _, p := range buckets {
		label := h.bucketLabel(p.idx)
		out = append(out, HistoBucket{
			Label: label,
			Count: p.count,
			Pct:   float64(p.count) / total * 100,
		})
	}
	return out
}

// bucketLabel 返回第 i 个桶的文本标签。
func (h *Histogram) bucketLabel(i int) string {
	switch i {
	case 0:
		return "0ns"
	case 1:
		return "1ns"
	case 2:
		return "2ns"
	}
	low := uint64(1 << (uint(i) - 1))
	high := uint64(1 << uint(i))
	// 对常见范围使用更友好的标签
	loDur := time.Duration(low)
	hiDur := time.Duration(high)
	loStr := FormatDuration(loDur)
	hiStr := FormatDuration(hiDur)
	return loStr + "~" + hiStr
}

// ── 百分位辅助：从全量样本生成统计（兼容保留，用于外部场景） ────

// ComputeLatencyFromSamples 从全量切片计算精确统计（非流式）。
// 适用于连接/认证延迟等样本数可控的场景。
func ComputeLatencyFromSamples(durs []time.Duration) LatencyStats {
	if len(durs) == 0 {
		return LatencyStats{}
	}
	sorted := make([]time.Duration, len(durs))
	copy(sorted, durs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	n := len(sorted)
	var sum int64
	for _, d := range sorted {
		sum += int64(d)
	}
	mean := time.Duration(sum / int64(n))

	var variance float64
	for _, d := range sorted {
		diff := float64(int64(d) - int64(mean))
		variance += diff * diff
	}
	variance /= float64(n)
	stdDev := time.Duration(math.Sqrt(variance))

	pick := func(p float64) time.Duration {
		idx := int(float64(n-1) * p)
		if idx < 0 {
			return sorted[0]
		}
		return sorted[idx]
	}

	// 合并数据到 Histogram 以复用 buildBuckets
	h := &Histogram{}
	for _, d := range sorted {
		h.mu.Lock()
		ns := int64(d)
		h.count++
		h.sum += ns
		h.sumSq += float64(ns) * float64(ns)
		if h.count == 1 || d < h.min {
			h.min = d
		}
		if d > h.max {
			h.max = d
		}
		if ns <= 0 {
			h.zero++
			h.buckets[0]++
		} else {
			idx := fls64(uint64(ns))
			if idx >= numLogBuckets {
				idx = numLogBuckets - 1
			}
			h.buckets[idx]++
		}
		h.mu.Unlock()
	}

	return LatencyStats{
		Count:   n,
		Min:     sorted[0],
		Max:     sorted[n-1],
		Mean:    mean,
		P50:     pick(0.50),
		P90:     pick(0.90),
		P95:     pick(0.95),
		P99:     pick(0.99),
		P999:    pick(0.999),
		StdDev:  stdDev,
		Zero:    0,
		Buckets: h.buildBucketsLocked(),
	}
}
