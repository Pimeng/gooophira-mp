package phira

import (
	"math/rand/v2"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/common/cache"
	"github.com/Pimeng/gooophira-mp/internal/core/server"
)

// refreshNearExpiryThreshold 是 KeysNearExpiry 的剩余 TTL 阈值：
// 缓存键剩余 TTL <= 1 小时即视为「快到期」，可被刷新 goroutine 选中失效。
const refreshNearExpiryThreshold = 1 * time.Hour

// refreshInvalidateN 是每个缓存每轮随机失效的键数上限。
// 取 5 是为了避免单轮失效过多导致大量 GetOrSet 并发回源。
const refreshInvalidateN = 5

// StartRefresh 启动后台 goroutine：每 interval 调用 KeysNearExpiry 取快到期键，
// 随机 Delete 几个，触发下次 GetOrSet 时重拉。
//
// state 当前未直接使用，保留参数以便未来接入 ServerState（如基于在线用户刷新）。
// 幂等：重复调用不会启动多个 goroutine（c.done != nil 即跳过）。
// 必须配合 Stop 释放资源（main.go 中 defer Stop）。
func (c *Client) StartRefresh(state *server.ServerState, interval time.Duration) {
	if c.done != nil {
		return // 已启动
	}
	c.done = make(chan struct{})
	go c.refreshLoop(state, interval)
}

// Stop 关闭后台刷新 goroutine 并等待退出。幂等：重复调用安全。
func (c *Client) Stop() {
	select {
	case <-c.stop:
		// 已关闭
	default:
		close(c.stop)
	}
	if c.done != nil {
		<-c.done
	}
}

func (c *Client) refreshLoop(state *server.ServerState, interval time.Duration) {
	defer close(c.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-ticker.C:
			c.refreshExpiring()
		}
	}
}

// refreshExpiring 对 tokenCache / recordCache / chartCache 各随机失效
// 最多 refreshInvalidateN 个快到期键。Redis 模式 KeysNearExpiry 返回 nil，自动 no-op。
func (c *Client) refreshExpiring() {
	invalidateRandom(tokenCache, refreshInvalidateN)
	invalidateRandom(recordCache, refreshInvalidateN)
	invalidateRandom(chartCache, refreshInvalidateN)
}

// invalidateRandom 从缓存中取快到期键，Fisher-Yates 随机选最多 n 个 Delete。
// 被删除的键下次 GetOrSet 时会重拉，达到被动刷新效果。
func invalidateRandom[K comparable, V any](c *cache.Cache[K, V], n int) {
	keys := c.KeysNearExpiry(refreshNearExpiryThreshold)
	if len(keys) == 0 {
		return
	}
	// Fisher-Yates 取前 n 个随机样本
	for i := 0; i < n && i < len(keys); i++ {
		j := i + rand.IntN(len(keys)-i)
		keys[i], keys[j] = keys[j], keys[i]
		c.Delete(keys[i])
	}
}
