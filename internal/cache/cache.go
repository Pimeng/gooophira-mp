// Package cache 提供带 TTL 的通用缓存：默认本地（内存采样 LRU + 防抖落盘），
// 配置启用 Redis 时切换为进程级共享的 Redis 后端（多实例间共享缓存）。
// 对应 TS server/utils/cache.ts。
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// lruSampleSize 是采样近似 LRU 的样本数（O(1) 淘汰，对齐 TS）。
const lruSampleSize = 5

// diskSaveDebounce 是落盘防抖间隔。
const diskSaveDebounce = 100 * time.Millisecond

func nowMS() int64 { return time.Now().UnixMilli() }

// accessCounter 是全局单调递增计数器，用于 lastAccessed（LRU 淘汰比较）。
// 用计数器而非时间戳：Windows time.Now() 分辨率约 15ms，快速连续 Set 会产生同时间戳，
// 导致 evictIfNeeded 淘汰结果非确定性。计数器保证每次访问严格递增，LRU 顺序确定。
var accessCounter atomic.Int64

func nextAccess() int64 { return accessCounter.Add(1) }

// storedEntry 是落盘 / Redis 中的序列化条目。
type storedEntry[V any] struct {
	Value    V     `json:"value"`
	CachedAt int64 `json:"cachedAt"`
}

type memEntry[V any] struct {
	value        V
	cachedAt     int64 // Unix 毫秒
	lastAccessed int64 // Unix 毫秒（内存 LRU 用）
}

// Options 是 Cache 的构造选项。
type Options struct {
	Name    string        // 键前缀 / 落盘文件名（如 "record_cache.json"）
	TTL     time.Duration // 0 = 永不过期
	MaxMem  int           // 内存条目上限（<=0 取默认 100）
	Persist bool          // 是否落盘（本地后端）
	Dir     string        // 落盘目录（Persist 时；空则取 "cache"）
}

// Cache 是带 TTL 的通用缓存：本地后端为内存（采样 LRU）+ 可选防抖落盘；
// 启用 Redis 时所有读写走进程级共享的 Redis（多实例共享）。并发安全。
type Cache[K comparable, V any] struct {
	name     string
	ttl      time.Duration
	maxMem   int
	persist  bool
	filePath string
	keyStr   func(K) string
	keyParse func(string) (K, bool)

	mu       sync.Mutex
	mem      map[K]memEntry[V]
	inFlight map[K]*flight[V]

	saveTimer *time.Timer
	pending   bool
}

type flight[V any] struct {
	done  chan struct{}
	value V
	err   error
}

// NewString 创建以 string 为键的缓存。
func NewString[V any](o Options) *Cache[string, V] {
	return newCache[string, V](o,
		func(s string) string { return s },
		func(s string) (string, bool) { return s, true })
}

// NewInt 创建以 int 为键的缓存。
func NewInt[V any](o Options) *Cache[int, V] {
	return newCache[int, V](o, strconv.Itoa,
		func(s string) (int, bool) { n, err := strconv.Atoi(s); return n, err == nil })
}

func newCache[K comparable, V any](o Options, keyStr func(K) string, keyParse func(string) (K, bool)) *Cache[K, V] {
	maxMem := o.MaxMem
	if maxMem <= 0 {
		maxMem = 100
	}
	dir := o.Dir
	if dir == "" {
		dir = "cache"
	}
	c := &Cache[K, V]{
		name:     o.Name,
		ttl:      o.TTL,
		maxMem:   maxMem,
		persist:  o.Persist,
		filePath: filepath.Join(dir, o.Name),
		keyStr:   keyStr,
		keyParse: keyParse,
		mem:      make(map[K]memEntry[V]),
		inFlight: make(map[K]*flight[V]),
	}
	if c.persist {
		c.loadFromDisk()
	}
	registerMigrator(c.migrateToRedis)
	return c
}

func (c *Cache[K, V]) redisKey(key K) string {
	return "cache:" + c.name + ":" + c.keyStr(key)
}

func (c *Cache[K, V]) expired(cachedAt int64) bool {
	return c.ttl > 0 && nowMS()-cachedAt > c.ttl.Milliseconds()
}

// Get 取缓存值；未命中或已过期返回 (zero, false)。Redis 出错按未命中降级。
func (c *Cache[K, V]) Get(key K) (V, bool) {
	if rc := getRedis(); rc != nil {
		return c.redisGet(rc, key)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.memGet(key)
}

func (c *Cache[K, V]) memGet(key K) (V, bool) {
	var zero V
	e, ok := c.mem[key]
	if !ok {
		return zero, false
	}
	if c.expired(e.cachedAt) {
		delete(c.mem, key)
		return zero, false
	}
	e.lastAccessed = nextAccess()
	c.mem[key] = e
	return e.value, true
}

func (c *Cache[K, V]) redisGet(rc *redis.Client, key K) (V, bool) {
	var zero V
	ctx, cancel := redisCtx()
	defer cancel()
	data, err := rc.Get(ctx, c.redisKey(key)).Bytes()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			reportRedisErr("get "+c.name, err)
		}
		return zero, false
	}
	var stored storedEntry[V]
	if err := json.Unmarshal(data, &stored); err != nil {
		reportRedisErr("unmarshal "+c.name, err)
		return zero, false
	}
	if c.expired(stored.CachedAt) {
		_ = rc.Del(ctx, c.redisKey(key)).Err()
		return zero, false
	}
	return stored.Value, true
}

// Set 写入缓存值。
func (c *Cache[K, V]) Set(key K, value V) {
	if rc := getRedis(); rc != nil {
		c.redisSet(rc, key, value)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mem[key] = memEntry[V]{value: value, cachedAt: nowMS(), lastAccessed: nextAccess()}
	c.evictIfNeeded()
	c.scheduleSaveLocked()
}

func (c *Cache[K, V]) redisSet(rc *redis.Client, key K, value V) {
	b, err := json.Marshal(storedEntry[V]{Value: value, CachedAt: nowMS()})
	if err != nil {
		reportRedisErr("marshal "+c.name, err)
		return
	}
	ctx, cancel := redisCtx()
	defer cancel()
	var exp time.Duration // 0 = 永不过期
	if c.ttl > 0 {
		exp = c.ttl
	}
	if err := rc.Set(ctx, c.redisKey(key), b, exp).Err(); err != nil {
		reportRedisErr("set "+c.name, err)
	}
}

// evictIfNeeded 在超出内存上限时按采样 LRU 淘汰一条（调用方持锁）。
func (c *Cache[K, V]) evictIfNeeded() {
	if len(c.mem) <= c.maxMem {
		return
	}
	keys := make([]K, 0, len(c.mem))
	for k := range c.mem {
		keys = append(keys, k)
	}
	sample := min(lruSampleSize, len(keys))
	var oldestKey K
	var oldestTime int64 = 1<<63 - 1
	found := false
	// Fisher-Yates 取前 sample 个随机样本，淘汰其中最久未访问者。
	for i := range sample {
		j := i + rand.IntN(len(keys)-i)
		keys[i], keys[j] = keys[j], keys[i]
		if e := c.mem[keys[i]]; e.lastAccessed < oldestTime {
			oldestTime = e.lastAccessed
			oldestKey = keys[i]
			found = true
		}
	}
	if found {
		delete(c.mem, oldestKey)
	}
}

// GetOrSet 取缓存；未命中则调 factory 生成并写入。并发请求同一 key 共享同一次 factory 调用。
func (c *Cache[K, V]) GetOrSet(key K, factory func() (V, error)) (V, error) {
	if v, ok := c.Get(key); ok {
		return v, nil
	}

	c.mu.Lock()
	if f, ok := c.inFlight[key]; ok {
		c.mu.Unlock()
		<-f.done
		return f.value, f.err
	}
	f := &flight[V]{done: make(chan struct{})}
	c.inFlight[key] = f
	c.mu.Unlock()

	v, err := factory()
	if err == nil {
		c.Set(key, v)
	}
	f.value, f.err = v, err
	close(f.done)

	c.mu.Lock()
	delete(c.inFlight, key)
	c.mu.Unlock()
	return v, err
}

// Delete 删除一个键。
func (c *Cache[K, V]) Delete(key K) {
	if rc := getRedis(); rc != nil {
		ctx, cancel := redisCtx()
		defer cancel()
		if err := rc.Del(ctx, c.redisKey(key)).Err(); err != nil {
			reportRedisErr("del "+c.name, err)
		}
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.mem, key)
	c.scheduleSaveLocked()
}

// Clear 清空缓存（Redis 模式按键前缀 SCAN+DEL）。
func (c *Cache[K, V]) Clear() {
	if rc := getRedis(); rc != nil {
		c.redisClear(rc)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mem = make(map[K]memEntry[V])
	if c.persist {
		c.stopSaveTimerLocked()
		c.flushToDiskLocked()
	}
}

// KeysNearExpiry 返回本地缓存中剩余 TTL <= remaining 的键（用于被动失效刷新）。
// Redis 模式返回 nil：Redis 自带 TTL 自动过期，无需主动刷新。
// 仅本地后端有效；remaining <= 0 时返回所有已缓存键（不论剩余 TTL）。
func (c *Cache[K, V]) KeysNearExpiry(remaining time.Duration) []K {
	if getRedis() != nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.mem) == 0 {
		return nil
	}
	now := nowMS()
	thresholdMS := (c.ttl - remaining).Milliseconds()
	var keys []K
	for k, e := range c.mem {
		if c.ttl > 0 && now-e.cachedAt >= thresholdMS {
			keys = append(keys, k)
		}
	}
	return keys
}

func (c *Cache[K, V]) redisClear(rc *redis.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pattern := "cache:" + c.name + ":*"
	var cursor uint64
	for {
		keys, next, err := rc.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			reportRedisErr("scan "+c.name, err)
			return
		}
		if len(keys) > 0 {
			if err := rc.Del(ctx, keys...).Err(); err != nil {
				reportRedisErr("clear "+c.name, err)
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
}

// ---------- 落盘（本地后端）----------

func (c *Cache[K, V]) loadFromDisk() {
	data, err := os.ReadFile(c.filePath)
	if err != nil {
		return // 文件不存在或读取失败，忽略
	}
	var parsed map[string]storedEntry[V]
	if json.Unmarshal(data, &parsed) != nil {
		return
	}
	for keyStr, e := range parsed {
		if c.expired(e.CachedAt) {
			continue
		}
		key, ok := c.keyParse(keyStr)
		if !ok {
			continue
		}
		c.mem[key] = memEntry[V]{value: e.Value, cachedAt: e.CachedAt, lastAccessed: nextAccess()}
	}
}

// scheduleSaveLocked 安排一次防抖落盘（调用方持锁）。
func (c *Cache[K, V]) scheduleSaveLocked() {
	if !c.persist {
		return
	}
	c.pending = true
	if c.saveTimer != nil {
		return
	}
	c.saveTimer = time.AfterFunc(diskSaveDebounce, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.saveTimer = nil
		if !c.pending {
			return
		}
		c.pending = false
		c.flushToDiskLocked()
	})
}

func (c *Cache[K, V]) stopSaveTimerLocked() {
	c.pending = false
	if c.saveTimer != nil {
		c.saveTimer.Stop()
		c.saveTimer = nil
	}
}

// flushToDiskLocked 把内存条目写入磁盘（调用方持锁）。
func (c *Cache[K, V]) flushToDiskLocked() {
	if err := os.MkdirAll(filepath.Dir(c.filePath), 0o755); err != nil {
		return
	}
	obj := make(map[string]storedEntry[V], len(c.mem))
	for k, e := range c.mem {
		obj[c.keyStr(k)] = storedEntry[V]{Value: e.value, CachedAt: e.cachedAt}
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return
	}
	tmp := c.filePath + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, c.filePath) // 原子替换
	}
}

// migrateToRedis 把内存数据迁移进 Redis 并清空本地内存/落盘（连接 Redis 成功后调用）。
func (c *Cache[K, V]) migrateToRedis() {
	rc := getRedis()
	if rc == nil {
		return
	}
	c.mu.Lock()
	entries := c.mem
	c.mem = make(map[K]memEntry[V])
	c.stopSaveTimerLocked()
	c.mu.Unlock()

	if len(entries) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	now := nowMS()
	pipe := rc.Pipeline()
	for k, e := range entries {
		if c.expired(e.cachedAt) {
			continue
		}
		b, err := json.Marshal(storedEntry[V]{Value: e.value, CachedAt: e.cachedAt})
		if err != nil {
			continue
		}
		var exp time.Duration
		if c.ttl > 0 {
			exp = max(c.ttl-time.Duration(now-e.cachedAt)*time.Millisecond, time.Millisecond)
		}
		pipe.Set(ctx, c.redisKey(k), b, exp)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		reportRedisErr("migrate "+c.name, err)
	}
}
