package server

import (
	"math"
	"testing"
)

// TestPickNextHost_EmptyList 验证空列表返回 false（无候选人）。
func TestPickNextHost_EmptyList(t *testing.T) {
	id, ok := pickNextHost(nil, 1)
	if ok {
		t.Errorf("empty list should return ok=false, got id=%d ok=true", id)
	}
	if id != 0 {
		t.Errorf("empty list should return id=0, got %d", id)
	}
}

// TestPickNextHost_SingleElement 验证单元素列表：无论 oldHostID 是多少都回环到唯一候选人。
func TestPickNextHost_SingleElement(t *testing.T) {
	ids := []int{42}
	// oldHostID 不在列表 → 回环到唯一元素
	if id, ok := pickNextHost(ids, 1); !ok || id != 42 {
		t.Errorf("single list, oldHost=1: want 42 true, got %d %v", id, ok)
	}
	// oldHostID 就是唯一元素 → 回环到自身（确定性 cycle 语义）
	if id, ok := pickNextHost(ids, 42); !ok || id != 42 {
		t.Errorf("single list, oldHost=42: want 42 true (wrap), got %d %v", id, ok)
	}
	// oldHostID 大于唯一元素 → 回环
	if id, ok := pickNextHost(ids, 100); !ok || id != 42 {
		t.Errorf("single list, oldHost=100: want 42 true (wrap), got %d %v", id, ok)
	}
}

// TestPickNextHost_PicksNextLarger 验证基本语义：选大于 oldHostID 的最小者。
func TestPickNextHost_PicksNextLarger(t *testing.T) {
	ids := []int{10, 20, 30, 40}
	if id, ok := pickNextHost(ids, 20); !ok || id != 30 {
		t.Errorf("oldHost=20: want 30 true, got %d %v", id, ok)
	}
	if id, ok := pickNextHost(ids, 10); !ok || id != 20 {
		t.Errorf("oldHost=10: want 20 true, got %d %v", id, ok)
	}
}

// TestPickNextHost_WrapAround 验证 oldHostID 是最大值时回环到最小 ID。
func TestPickNextHost_WrapAround(t *testing.T) {
	ids := []int{10, 20, 30, 40}
	if id, ok := pickNextHost(ids, 40); !ok || id != 10 {
		t.Errorf("oldHost=max(40): want 10 true (wrap), got %d %v", id, ok)
	}
	// oldHostID 比最大值还大 → 同样回环
	if id, ok := pickNextHost(ids, 999); !ok || id != 10 {
		t.Errorf("oldHost=999: want 10 true (wrap), got %d %v", id, ok)
	}
}

// TestPickNextHost_OldHostNotInList 验证 oldHostID 不在列表中时取大于它的最小者。
func TestPickNextHost_OldHostNotInList(t *testing.T) {
	ids := []int{10, 20, 30, 40}
	// oldHostID=25 不在列表 → 取下一个大于 25 的，即 30
	if id, ok := pickNextHost(ids, 25); !ok || id != 30 {
		t.Errorf("oldHost=25 (not in list): want 30 true, got %d %v", id, ok)
	}
	// oldHostID=5 比所有都小 → 取最小的大于 5 的，即 10
	if id, ok := pickNextHost(ids, 5); !ok || id != 10 {
		t.Errorf("oldHost=5 (below all): want 10 true, got %d %v", id, ok)
	}
}

// TestPickNextHost_DoesNotMutateInput 验证输入切片不被修改。
func TestPickNextHost_DoesNotMutateInput(t *testing.T) {
	ids := []int{30, 10, 40, 20}
	original := append([]int(nil), ids...)
	_, _ = pickNextHost(ids, 20)
	for i := range ids {
		if ids[i] != original[i] {
			t.Fatalf("input mutated at index %d: got %d, want %d", i, ids[i], original[i])
		}
	}
}

// TestPickNextHost_UnsortedInput 验证乱序输入也能正确排序选主。
func TestPickNextHost_UnsortedInput(t *testing.T) {
	ids := []int{40, 10, 30, 20}
	if id, ok := pickNextHost(ids, 20); !ok || id != 30 {
		t.Errorf("unsorted oldHost=20: want 30 true, got %d %v", id, ok)
	}
	if id, ok := pickNextHost(ids, 40); !ok || id != 10 {
		t.Errorf("unsorted oldHost=40: want 10 true (wrap), got %d %v", id, ok)
	}
}

// TestPickNextHost_DuplicateIDs 验证重复 ID 输入：仍能选出大于 oldHostID 的最小者。
// 实际场景不应出现重复 ID，但函数应 gracefully 处理而非 panic。
func TestPickNextHost_DuplicateIDs(t *testing.T) {
	ids := []int{10, 20, 20, 30}
	if id, ok := pickNextHost(ids, 20); !ok || id != 30 {
		t.Errorf("dup oldHost=20: want 30 true, got %d %v", id, ok)
	}
}

// TestPickNextHost_NegativeIDs 验证负 ID 输入（虽然实际用户 ID 都为正，但函数应保持确定性）。
func TestPickNextHost_NegativeIDs(t *testing.T) {
	ids := []int{-10, -5, 0, 5}
	if id, ok := pickNextHost(ids, -5); !ok || id != 0 {
		t.Errorf("neg oldHost=-5: want 0 true, got %d %v", id, ok)
	}
	if id, ok := pickNextHost(ids, 5); !ok || id != -10 {
		t.Errorf("neg oldHost=5 (max): want -10 true (wrap), got %d %v", id, ok)
	}
}

// TestPickNextHost_DeterministicAcrossCalls 验证多次调用结果一致（确定性是核心契约）。
func TestPickNextHost_DeterministicAcrossCalls(t *testing.T) {
	ids := []int{30, 10, 40, 20}
	first, _ := pickNextHost(ids, 20)
	for i := 0; i < 10; i++ {
		got, _ := pickNextHost(ids, 20)
		if got != first {
			t.Fatalf("non-deterministic: first=%d, then got=%d on iter %d", first, got, i)
		}
	}
}

// TestPickNextHost_FullCycle 验证完整轮转：从最小 ID 开始，逐次推进，最终回环到最小 ID。
func TestPickNextHost_FullCycle(t *testing.T) {
	ids := []int{10, 20, 30}
	want := []int{20, 30, 10} // oldHost=10→20, 20→30, 30→10(回环)
	oldHost := 10
	for i, w := range want {
		next, ok := pickNextHost(ids, oldHost)
		if !ok || next != w {
			t.Fatalf("cycle step %d: oldHost=%d want=%d, got %d %v", i, oldHost, w, next, ok)
		}
		oldHost = next
	}
}

// TestPickNextHost_MinInt32 验证极小 ID（接近 int32 边界）不会溢出或异常。
func TestPickNextHost_MinInt32(t *testing.T) {
	ids := []int{math.MinInt32, -1, math.MaxInt32}
	if id, ok := pickNextHost(ids, math.MinInt32); !ok || id != -1 {
		t.Errorf("oldHost=MinInt32: want -1 true, got %d %v", id, ok)
	}
	// oldHost=MaxInt32 → 回环到 MinInt32
	if id, ok := pickNextHost(ids, math.MaxInt32); !ok || id != math.MinInt32 {
		t.Errorf("oldHost=MaxInt32: want MinInt32 true (wrap), got %d %v", id, ok)
	}
}
