package cache

import (
	"strconv"
	"testing"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/alicebob/miniredis/v2"
)

// startMiniRedis 启动一个进程内 Redis（miniredis）并把缓存切到该后端，测试结束自动还原。
func startMiniRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	port, _ := strconv.Atoi(mr.Port())
	if err := InitRedis(&config.RedisConfig{Enabled: true, Host: mr.Host(), Port: port}); err != nil {
		t.Fatalf("InitRedis(miniredis): %v", err)
	}
	t.Cleanup(CloseRedis)
	return mr
}

func TestRedis_SetGetDeleteClear(t *testing.T) {
	startMiniRedis(t)
	if !RedisEnabled() {
		t.Fatal("RedisEnabled should be true with miniredis connected")
	}

	c := NewInt[string](Options{Name: "rtest-setget", TTL: time.Hour})
	if _, ok := c.Get(1); ok {
		t.Fatal("fresh key should miss")
	}
	c.Set(1, "one")
	c.Set(2, "two")
	if v, ok := c.Get(1); !ok || v != "one" {
		t.Fatalf("redis get(1) = %v %v, want one", v, ok)
	}

	c.Delete(1)
	if _, ok := c.Get(1); ok {
		t.Error("deleted key should miss")
	}
	if _, ok := c.Get(2); !ok {
		t.Error("key 2 should still be present")
	}

	c.Clear() // SCAN+DEL 按前缀清理
	if _, ok := c.Get(2); ok {
		t.Error("Clear should drop all keys for this cache")
	}
}

func TestRedis_TTLExpiry(t *testing.T) {
	mr := startMiniRedis(t)
	c := NewString[int](Options{Name: "rtest-ttl", TTL: 30 * time.Second})
	c.Set("k", 99)
	if v, ok := c.Get("k"); !ok || v != 99 {
		t.Fatalf("get = %v %v", v, ok)
	}
	// miniredis 不会自动推进时间——手动快进越过 TTL。
	mr.FastForward(31 * time.Second)
	if _, ok := c.Get("k"); ok {
		t.Error("entry should expire after TTL via Redis pexpire")
	}
}

func TestRedis_KeyNamespacing(t *testing.T) {
	startMiniRedis(t)
	a := NewInt[string](Options{Name: "rtest-nsa", TTL: time.Hour})
	b := NewInt[string](Options{Name: "rtest-nsb", TTL: time.Hour})
	a.Set(1, "from-a")
	b.Set(1, "from-b")
	// 同 key 不同缓存命名空间应互不干扰。
	if v, _ := a.Get(1); v != "from-a" {
		t.Errorf("cache a leaked: %q", v)
	}
	if v, _ := b.Get(1); v != "from-b" {
		t.Errorf("cache b leaked: %q", v)
	}
	a.Clear()
	if _, ok := b.Get(1); !ok {
		t.Error("clearing cache a must not affect cache b")
	}
}

func TestRedis_MigrateOnConnect(t *testing.T) {
	// 连接 Redis 前先在本地内存写入数据，连接后应迁移过去。
	CloseRedis() // 确保从本地态开始
	c := NewInt[string](Options{Name: "rtest-migrate", TTL: time.Hour})
	c.Set(10, "local-value") // 此时仅写入本地内存

	mr := miniredis.RunT(t)
	port, _ := strconv.Atoi(mr.Port())
	if err := InitRedis(&config.RedisConfig{Enabled: true, Host: mr.Host(), Port: port}); err != nil {
		t.Fatalf("InitRedis: %v", err)
	}
	t.Cleanup(CloseRedis)

	// 迁移后应能从 Redis 读到原本地数据。
	if v, ok := c.Get(10); !ok || v != "local-value" {
		t.Errorf("pre-existing local entry should migrate to Redis, got %v %v", v, ok)
	}
}
