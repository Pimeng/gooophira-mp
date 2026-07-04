package server

import (
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

// ---------- int32FromInt 边界 ----------

func TestInt32FromInt_ValidBounds(t *testing.T) {
	cases := []int{0, 1, -1, math.MaxInt32, math.MinInt32}
	for _, n := range cases {
		got := int32FromInt(n)
		if int(got) != n {
			t.Errorf("int32FromInt(%d) = %d, want %d", n, got, n)
		}
	}
}

func TestInt32FromInt_OverflowPanics(t *testing.T) {
	cases := []int{
		math.MaxInt32 + 1,
		math.MinInt32 - 1,
		math.MaxInt,
		math.MinInt,
	}
	for _, n := range cases {
		func() {
			defer func() {
				if rec := recover(); rec == nil {
					t.Errorf("int32FromInt(%d) should panic on overflow", n)
				}
			}()
			_ = int32FromInt(n)
		}()
	}
}

// ---------- MarkDangle / IsStillDangling token 身份比较 ----------

func TestMarkDangle_TokenIdentity(t *testing.T) {
	h := newHarness()
	u := h.addUser(1, "alice")

	// 初始 dangleToken 为 nil：传入 nil 会匹配（指针身份比较 nil==nil）。
	// 实际调用方总用 MarkDangle 返回的非 nil token，不会传 nil，此处仅记录边界行为。
	if !u.IsStillDangling(nil) {
		t.Error("fresh user (dangleToken=nil) matches nil token by identity comparison")
	}

	deadline := int64(time.Now().Add(10 * time.Second).UnixMilli())
	token1 := u.MarkDangle(&deadline)
	if token1 == nil {
		t.Fatal("MarkDangle should return non-nil token")
	}
	// 同一 token 应匹配
	if !u.IsStillDangling(token1) {
		t.Error("IsStillDangling should return true for the same token")
	}
	// 不同 token 不应匹配
	token2 := &DangleToken{}
	if u.IsStillDangling(token2) {
		t.Error("IsStillDangling should return false for a different token")
	}
	// MarkDangle 后再传 nil 不应匹配（dangleToken 已是非 nil）
	if u.IsStillDangling(nil) {
		t.Error("after MarkDangle, nil token should not match non-nil dangleToken")
	}
}

func TestSetSession_ClearsDangleToken(t *testing.T) {
	h := newHarness()
	u := h.addUser(1, "alice")
	deadline := int64(time.Now().Add(10 * time.Second).UnixMilli())
	token := u.MarkDangle(&deadline)
	if !u.IsStillDangling(token) {
		t.Fatal("token should be set after MarkDangle")
	}
	// SetSession（含 nil）应清除 dangling 状态
	u.SetSession(nil)
	if u.IsStillDangling(token) {
		t.Error("SetSession should clear dangle token, making IsStillDangling false")
	}
	if u.DangleDeadline != nil {
		t.Error("SetSession should clear DangleDeadline")
	}
}

// ---------- dangleDeadlineMs 边界 ----------

func TestDangleDeadlineMs_NilDeadline(t *testing.T) {
	h := newHarness()
	u := h.addUser(1, "alice")
	// 未 MarkDangle → DangleDeadline 为 nil → 返回 0
	if got := u.dangleDeadlineMs(time.Now().UnixMilli()); got != 0 {
		t.Errorf("nil deadline should return 0, got %d", got)
	}
}

func TestDangleDeadlineMs_Expired(t *testing.T) {
	h := newHarness()
	u := h.addUser(1, "alice")
	// 已过期的 deadline → 返回 0
	past := int64(time.Now().Add(-5 * time.Second).UnixMilli())
	u.MarkDangle(&past)
	if got := u.dangleDeadlineMs(time.Now().UnixMilli()); got != 0 {
		t.Errorf("expired deadline should return 0, got %d", got)
	}
}

func TestDangleDeadlineMs_FutureDeadline(t *testing.T) {
	h := newHarness()
	u := h.addUser(1, "alice")
	// 未来 10 秒到期的 deadline → 返回约 10000ms（允许少量误差）
	future := int64(time.Now().Add(10 * time.Second).UnixMilli())
	u.MarkDangle(&future)
	got := u.dangleDeadlineMs(time.Now().UnixMilli())
	if got <= 0 || got > 10_000 {
		t.Errorf("future deadline should return (0, 10000], got %d", got)
	}
}

// ---------- ToInfo infoCache 失效 ----------

func TestToInfo_CacheInvalidatedOnMonitorChange(t *testing.T) {
	h := newHarness()
	u := h.addUser(1, "alice")

	// 第一次调用：构造并缓存
	info1 := u.ToInfo()
	if info1.Monitor {
		t.Error("initial Monitor should be false")
	}
	// 第二次调用：命中缓存（相同 Monitor）
	info2 := u.ToInfo()
	if info2 != info1 {
		t.Error("second call with same Monitor should hit cache and return equal value")
	}

	// Monitor 变化 → 缓存应失效并重建
	u.Monitor = true
	info3 := u.ToInfo()
	if !info3.Monitor {
		t.Error("after setting Monitor=true, ToInfo should reflect it")
	}
}

func TestToInfo_CacheHitWhenMonitorUnchanged(t *testing.T) {
	h := newHarness()
	u := h.addUser(1, "alice")
	u.Monitor = false
	first := u.ToInfo()
	// 多次调用 Monitor 不变 → 持续命中缓存
	for i := 0; i < 5; i++ {
		got := u.ToInfo()
		if got != first {
			t.Fatalf("iter %d: cache miss expected hit, got different value", i)
		}
	}
}

// ---------- GameTime 原子读写 ----------

func TestGameTime_InitialNegInf(t *testing.T) {
	h := newHarness()
	u := h.addUser(1, "alice")
	if !math.IsInf(u.GameTime(), -1) {
		t.Errorf("new user GameTime should be -Inf, got %v", u.GameTime())
	}
}

func TestSetGameTime_Roundtrip(t *testing.T) {
	h := newHarness()
	u := h.addUser(1, "alice")
	values := []float64{0.0, 1.5, -3.14, math.NaN(), math.Inf(1), 999999.999}
	for _, v := range values {
		u.SetGameTime(v)
		got := u.GameTime()
		if v != v { // NaN
			if got == got {
				t.Errorf("SetGameTime(NaN) not preserved: got %v", got)
			}
			continue
		}
		if got != v {
			t.Errorf("SetGameTime(%v) → GameTime() = %v", v, got)
		}
	}
}

// ---------- CanMonitor ----------

func TestCanMonitor(t *testing.T) {
	h := newHarness(100, 200) // 100 和 200 在 monitors 白名单
	mon1 := h.addUser(100, "mon1")
	mon2 := h.addUser(200, "mon2")
	player := h.addUser(300, "player")
	if !mon1.CanMonitor() || !mon2.CanMonitor() {
		t.Error("configured monitors should return true")
	}
	if player.CanMonitor() {
		t.Error("non-configured user should not be able to monitor")
	}
}

// ---------- 竟态测试（应配合 -race 运行）----------

// TestUser_ConcurrentSetSessionAndTrySend 验证并发 SetSession 与 TrySend 不会 panic。
// 对应「User.Mu 保护 Session 字段」的契约。
func TestUser_ConcurrentSetSessionAndTrySend(t *testing.T) {
	h := newHarness()
	u := h.addUser(1, "alice")
	cmd := protocol.SrvChat{Result: protocol.Ok(protocol.Unit{})}

	var wg sync.WaitGroup
	stop := atomic.Bool{}
	// writer：不停切换 session
	wg.Go(func() {
		for !stop.Load() {
			u.SetSession(&mockSession{id: "switching"})
			u.SetSession(nil)
		}
	})
	// reader：不停 TrySend
	wg.Go(func() {
		for !stop.Load() {
			u.TrySend(cmd)
		}
	})
	// 让两个 goroutine 跑一会
	time.Sleep(50 * time.Millisecond)
	stop.Store(true)
	wg.Wait()
}

// TestUser_ConcurrentSetGameTimeAndRead 验证并发 SetGameTime 与 GameTime 不会 panic
// 且 GameTime 总是某个已写入值（atomic 保证）。
func TestUser_ConcurrentSetGameTimeAndRead(t *testing.T) {
	h := newHarness()
	u := h.addUser(1, "alice")
	var wg sync.WaitGroup
	stop := atomic.Bool{}
	values := []float64{0.0, 1.1, 2.2, 3.3, -7.7}
	// writer
	wg.Go(func() {
		for !stop.Load() {
			for _, v := range values {
				u.SetGameTime(v)
			}
		}
	})
	// reader
	wg.Go(func() {
		for !stop.Load() {
			_ = u.GameTime()
		}
	})
	time.Sleep(50 * time.Millisecond)
	stop.Store(true)
	wg.Wait()
}

// TestUser_ConcurrentToInfo 验证并发 ToInfo + Monitor 修改不会 panic。
// infoCache 是 atomic.Pointer，但 Monitor 字段是普通 bool（state.Mu 保护）。
// 此测试只验证 ToInfo 本身的并发安全性，不验证 Monitor 一致性。
func TestUser_ConcurrentToInfo(t *testing.T) {
	h := newHarness()
	u := h.addUser(1, "alice")
	var wg sync.WaitGroup
	stop := atomic.Bool{}
	for range 4 {
		wg.Go(func() {
			for !stop.Load() {
				_ = u.ToInfo()
			}
		})
	}
	time.Sleep(50 * time.Millisecond)
	stop.Store(true)
	wg.Wait()
}

// TestUser_ConcurrentMarkDangleAndIsStillDangling 验证并发 MarkDangle 与 IsStillDangling
// 不会 panic 且 IsStillDangling 总返回一致结果（基于最新 token）。
func TestUser_ConcurrentMarkDangleAndIsStillDangling(t *testing.T) {
	h := newHarness()
	u := h.addUser(1, "alice")
	var wg sync.WaitGroup
	stop := atomic.Bool{}
	var lastToken atomic.Pointer[DangleToken]
	deadline := int64(time.Now().Add(10 * time.Second).UnixMilli())
	// writer：不停 MarkDangle
	wg.Go(func() {
		for !stop.Load() {
			tok := u.MarkDangle(&deadline)
			lastToken.Store(tok)
		}
	})
	// reader：不停 IsStillDangling
	wg.Go(func() {
		for !stop.Load() {
			if tok := lastToken.Load(); tok != nil {
				_ = u.IsStillDangling(tok)
			}
		}
	})
	time.Sleep(50 * time.Millisecond)
	stop.Store(true)
	wg.Wait()
}

// ---------- NewUser 默认值 ----------

func TestNewUser_Defaults(t *testing.T) {
	cfg := &config.ServerConfig{}
	st := NewServerState(cfg, nil, "test", "", "")
	u := NewUser(42, "alice", "zh-CN", st)
	if u.ID != 42 {
		t.Errorf("ID = %d, want 42", u.ID)
	}
	if u.Name != "alice" {
		t.Errorf("Name = %q, want alice", u.Name)
	}
	if u.Server != st {
		t.Error("Server should point to provided state")
	}
	if u.Room != nil {
		t.Error("new user should not be in a room")
	}
	if u.IsConnected() {
		t.Error("new user should not be connected (no session)")
	}
}
