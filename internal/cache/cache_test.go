package cache

import (
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
)

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
	c := NewString[string](Options{Name: "t.json"}) // TTL 0
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
