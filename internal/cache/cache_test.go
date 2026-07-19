package cache

import (
	"errors"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
)

var errOp = errors.New("simulated redis operation failure")

func TestCache_GetSetTTL(t *testing.T) {
	c := NewString[int](Options{Name: "t.json", TTL: time.Hour})
	if _, ok := c.Get("a"); ok {
		t.Fatal("empty cache should miss")
	}
	c.Set("a", 42)
	if v, ok := c.Get("a"); !ok || v != 42 {
		t.Fatalf("expected hit 42, got %v %v", v, ok)
	}

	// 把 cachedAt 回拨到 TTL 之外 → 应判定过期。
	c.mu.Lock()
	e := c.mem["a"]
	e.cachedAt = nowMS() - c.ttl.Milliseconds() - 1000
	c.mem["a"] = e
	c.mu.Unlock()
	if _, ok := c.Get("a"); ok {
		t.Error("expired entry should miss")
	}
}

func TestCache_NoTTLNeverExpires(t *testing.T) {
	c := NewString[string](Options{Name: "t.json"}) // TTL 为 0。
	c.Set("k", "v")
	c.mu.Lock()
	e := c.mem["k"]
	e.cachedAt = 1 // 很久以前
	c.mem["k"] = e
	c.mu.Unlock()
	if v, ok := c.Get("k"); !ok || v != "v" {
		t.Error("ttl=0 should never expire")
	}
}

func TestCache_LRUEviction(t *testing.T) {
	c := NewInt[int](Options{Name: "t.json", MaxMem: 3})
	for i := range 6 {
		c.Set(i, i*10)
	}
	c.mu.Lock()
	size := len(c.mem)
	c.mu.Unlock()
	if size > 3 {
		t.Errorf("memory size should stay within MaxMem=3, got %d", size)
	}
}

func TestCache_GetOrSetDedup(t *testing.T) {
	c := NewString[int](Options{Name: "t.json", TTL: time.Hour})
	var calls atomic.Int32
	factory := func() (int, error) {
		calls.Add(1)
		time.Sleep(40 * time.Millisecond) // 让并发请求都落到 in-flight 等待
		return 7, nil
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			v, err := c.GetOrSet("key", factory)
			if err != nil || v != 7 {
				t.Errorf("GetOrSet = %v,%v", v, err)
			}
		})
	}
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Errorf("factory should be called once under concurrency, got %d", got)
	}
	// 已缓存：再取应命中且不再调用 factory。
	if v, ok := c.Get("key"); !ok || v != 7 {
		t.Errorf("value should be cached after GetOrSet, got %v %v", v, ok)
	}
}

func TestCache_GetOrSetFactoryErrorNotCached(t *testing.T) {
	c := NewString[int](Options{Name: "t.json", TTL: time.Hour})
	wantErr := os.ErrNotExist
	if _, err := c.GetOrSet("k", func() (int, error) { return 0, wantErr }); err != wantErr {
		t.Fatalf("error should propagate, got %v", err)
	}
	if _, ok := c.Get("k"); ok {
		t.Error("failed factory result must not be cached")
	}
}

func TestCache_DiskPersistRoundtrip(t *testing.T) {
	dir := t.TempDir()
	c := NewInt[string](Options{Name: "rec.json", TTL: time.Hour, Persist: true, Dir: dir})
	c.Set(1, "one")
	c.Set(2, "two")
	// 强制立即落盘（绕过 100ms 防抖）。
	c.mu.Lock()
	c.flushToDiskLocked()
	c.mu.Unlock()

	// 新实例从同目录加载。
	c2 := NewInt[string](Options{Name: "rec.json", TTL: time.Hour, Persist: true, Dir: dir})
	if v, ok := c2.Get(1); !ok || v != "one" {
		t.Errorf("reloaded[1] = %v %v, want one", v, ok)
	}
	if v, ok := c2.Get(2); !ok || v != "two" {
		t.Errorf("reloaded[2] = %v %v, want two", v, ok)
	}
}

func TestCache_DiskSkipsExpiredOnLoad(t *testing.T) {
	dir := t.TempDir()
	c := NewInt[string](Options{Name: "rec.json", TTL: 50 * time.Millisecond, Persist: true, Dir: dir})
	c.Set(1, "fresh")
	c.mu.Lock()
	e := c.mem[1]
	e.cachedAt = nowMS() - 10_000 // 远超 50ms TTL
	c.mem[1] = e
	c.flushToDiskLocked()
	c.mu.Unlock()

	c2 := NewInt[string](Options{Name: "rec.json", TTL: 50 * time.Millisecond, Persist: true, Dir: dir})
	if _, ok := c2.Get(1); ok {
		t.Error("expired entry should not survive disk reload")
	}
}

func TestCache_Clear(t *testing.T) {
	c := NewString[int](Options{Name: "t.json", TTL: time.Hour})
	c.Set("a", 1)
	c.Set("b", 2)
	c.Clear()
	if _, ok := c.Get("a"); ok {
		t.Error("Clear should drop all entries")
	}
}

// TestCache_RedisBackend 仅在设置 REDIS_TEST_ADDR（如 127.0.0.1:6379）时运行。
func TestCache_RedisBackend(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("set REDIS_TEST_ADDR to run Redis-backed cache test")
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("REDIS_TEST_ADDR must be host:port, got %q", addr)
	}
	port, _ := strconv.Atoi(portStr)
	cfg := &config.RedisConfig{Enabled: true, Host: host, Port: port}
	if err := InitRedis(cfg); err != nil {
		t.Fatalf("InitRedis: %v", err)
	}
	defer CloseRedis()
	if !RedisEnabled() {
		t.Fatal("RedisEnabled should be true after InitRedis")
	}

	c := NewInt[string](Options{Name: "redis_test.json", TTL: time.Hour})
	c.Clear() // 清掉上次残留
	if _, ok := c.Get(99); ok {
		t.Fatal("fresh key should miss")
	}
	c.Set(99, "hello")
	if v, ok := c.Get(99); !ok || v != "hello" {
		t.Fatalf("redis get = %v %v, want hello", v, ok)
	}
	c.Delete(99)
	if _, ok := c.Get(99); ok {
		t.Error("deleted key should miss")
	}
	c.Clear()
}

// ---------- TTL 边界 ----------

// TestCache_TTLJustExpired 验证刚好过期（cachedAt + TTL == now）会被判过期。
func TestCache_TTLJustExpired(t *testing.T) {
	c := NewString[int](Options{Name: "t.json", TTL: time.Hour})
	c.Set("k", 1)
	// 把 cachedAt 设为 (now - TTL - 1ms)，确保 strictly expired
	c.mu.Lock()
	e := c.mem["k"]
	e.cachedAt = nowMS() - c.ttl.Milliseconds() - 1
	c.mem["k"] = e
	c.mu.Unlock()
	if _, ok := c.Get("k"); ok {
		t.Error("entry just past TTL should be expired")
	}
}

// TestCache_TTLAlmostExpiring 验证差 1ms 未过期仍命中。
func TestCache_TTLAlmostExpiring(t *testing.T) {
	c := NewString[int](Options{Name: "t.json", TTL: time.Hour})
	c.Set("k", 1)
	// 把 cachedAt 设为 (now - TTL + 1ms)，还差 1ms 才过期
	c.mu.Lock()
	e := c.mem["k"]
	e.cachedAt = nowMS() - c.ttl.Milliseconds() + 1
	c.mem["k"] = e
	c.mu.Unlock()
	if v, ok := c.Get("k"); !ok || v != 1 {
		t.Errorf("entry 1ms before TTL expiry should hit, got %v %v", v, ok)
	}
}

// ---------- GetOrSet 并发不同 key 互不干扰 ----------

// TestCache_GetOrSetDifferentKeysParallel 验证并发不同 key 不会互相影响。
func TestCache_GetOrSetDifferentKeysParallel(t *testing.T) {
	c := NewInt[int](Options{Name: "t.json", TTL: time.Hour})
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			key := i
			v, err := c.GetOrSet(key, func() (int, error) { return key * 10, nil })
			if err != nil || v != key*10 {
				t.Errorf("GetOrSet(%d) = %v, %v", key, v, err)
			}
		})
	}
	wg.Wait()
	// 验证所有 key 都已缓存
	for i := 0; i < 20; i++ {
		if v, ok := c.Get(i); !ok || v != i*10 {
			t.Errorf("after parallel GetOrSet, Get(%d) = %v %v, want %d true", i, v, ok, i*10)
		}
	}
}

// ---------- 并发 Set 同 key 不 panic ----------

func TestCache_ConcurrentSetSameKey(t *testing.T) {
	c := NewString[int](Options{Name: "t.json", TTL: time.Hour})
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			c.Set("k", 42)
		})
	}
	wg.Wait()
	if v, ok := c.Get("k"); !ok || v != 42 {
		t.Errorf("after concurrent Set, Get = %v %v, want 42 true", v, ok)
	}
}

// ---------- Clear 与 Set 并发不 panic ----------

func TestCache_ConcurrentClearAndSet(t *testing.T) {
	c := NewInt[int](Options{Name: "t.json", TTL: time.Hour})
	var wg sync.WaitGroup
	stop := make(chan struct{})
	// setter：不停 Set
	wg.Go(func() {
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
				c.Set(i%100, i)
				i++
			}
		}
	})
	// clearer：周期性 Clear
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				c.Clear()
			}
		}
	})
	// 读取协程：不停调用 Get。
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				c.Get(50)
			}
		}
	})
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// ---------- Delete 不存在的 key 不 panic ----------

func TestCache_DeleteNonExistentNoOp(t *testing.T) {
	c := NewString[int](Options{Name: "t.json", TTL: time.Hour})
	// 不应 panic
	c.Delete("nope")
	c.Delete("")
	// 已存在的 key 删除后再删一次也不 panic
	c.Set("k", 1)
	c.Delete("k")
	c.Delete("k") // 二次删除
	if _, ok := c.Get("k"); ok {
		t.Error("deleted key should miss")
	}
}

// ---------- evictIfNeeded maxMem 边界 ----------

// TestCache_EvictAtMaxMem1 验证 maxMem=1 时每次 Set 第二个 key 都会触发淘汰。
func TestCache_EvictAtMaxMem1(t *testing.T) {
	c := NewInt[int](Options{Name: "t.json", MaxMem: 1})
	c.Set(1, 100)
	c.Set(2, 200) // 应淘汰 1 或保持 2
	c.mu.Lock()
	size := len(c.mem)
	c.mu.Unlock()
	if size > 1 {
		t.Errorf("maxMem=1: size should stay ≤1, got %d", size)
	}
	// 至少 key=2 应该存在
	if v, ok := c.Get(2); !ok || v != 200 {
		t.Errorf("after Set(2), Get(2) = %v %v, want 200 true", v, ok)
	}
}

// TestCache_EvictKeepsAtLeastMaxMem 验证内存条目数始终 ≤ maxMem。
func TestCache_EvictKeepsAtLeastMaxMem(t *testing.T) {
	c := NewInt[int](Options{Name: "t.json", MaxMem: 5})
	for i := 0; i < 100; i++ {
		c.Set(i, i)
	}
	c.mu.Lock()
	size := len(c.mem)
	c.mu.Unlock()
	if size > 5 {
		t.Errorf("maxMem=5: size should stay ≤5 after 100 Sets, got %d", size)
	}
}

// ---------- migrateToRedis 空数据 no-op ----------

// TestCache_MigrateEmptyNoOp 验证空缓存的 migrateToRedis 不会 panic 或写错误。
func TestCache_MigrateEmptyNoOp(t *testing.T) {
	c := NewInt[int](Options{Name: "t_migrate_empty", TTL: time.Hour})
	// 内存为空时调用 migrateToRedis（不连 Redis，getRedis 返回 nil → 直接返回）
	c.migrateToRedis()
	// 不应 panic，内存仍为空
	c.mu.Lock()
	size := len(c.mem)
	c.mu.Unlock()
	if size != 0 {
		t.Errorf("migrateToRedis on empty cache should keep mem empty, got %d", size)
	}
}

// ---------- reportRedisErr 限频窗口 ----------

// TestRedis_ReportErrRateLimit 验证 reportRedisErr 限频：首次必报，30s 窗口内重复静默。
func TestRedis_ReportErrRateLimit(t *testing.T) {
	// 重置限频窗口起始时间
	lastRedisErrLog.Store(0)
	cl := &captureLogger{}
	SetLogger(cl)
	t.Cleanup(func() { SetLogger(nil); lastRedisErrLog.Store(0) })

	// 首次报告
	reportRedisErr("op1", errOp)
	first := cl.warns.Load()
	if first != 1 {
		t.Fatalf("first error should be reported, got %d warns", first)
	}
	// 窗口内重复：不应再报
	for i := 0; i < 5; i++ {
		reportRedisErr("op-repeat", errOp)
	}
	if got := cl.warns.Load(); got != 1 {
		t.Errorf("rate-limited errors should not be reported, got %d warns", got)
	}
	// nil err 不报
	reportRedisErr("op-nil", nil)
	if got := cl.warns.Load(); got != 1 {
		t.Errorf("nil err should not be reported, got %d warns", got)
	}
}

// TestRedis_ReportErrAfterWindowExpires 验证窗口外（模拟时间推进）后再次报告。
func TestRedis_ReportErrAfterWindowExpires(t *testing.T) {
	// 模拟「上一次报告在 31s 前」
	lastRedisErrLog.Store(time.Now().UnixMilli() - (redisErrLogIntervalMs + 1000))
	cl := &captureLogger{}
	SetLogger(cl)
	t.Cleanup(func() { SetLogger(nil); lastRedisErrLog.Store(0) })

	reportRedisErr("op-after-window", errOp)
	if got := cl.warns.Load(); got != 1 {
		t.Errorf("error after window expiry should be reported, got %d warns", got)
	}
}

// TestRedis_ReportErrNoLoggerSilent 验证未设置 logger 时静默降级。
func TestRedis_ReportErrNoLoggerSilent(t *testing.T) {
	SetLogger(nil)
	t.Cleanup(func() { lastRedisErrLog.Store(0) })
	// 不应 panic
	reportRedisErr("op-no-logger", errOp)
}
