package cache

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/redis/go-redis/v9"
)

// redisOpTimeout 是单次 Redis 操作的超时；超时即按缓存未命中降级（不阻塞业务）。
const redisOpTimeout = 3 * time.Second

var (
	redisMu     sync.RWMutex
	redisClient *redis.Client

	migratorsMu sync.Mutex
	migrators   []func() // 各缓存的「迁移到 Redis」回调（连接成功后逐个调用）
)

// 静默 go-redis 内部日志（连接池重试等）——连接错误由 InitRedis 统一处理并经服务端日志器上报。
type silentRedisLogger struct{}

func (silentRedisLogger) Printf(context.Context, string, ...any) {}

func init() { redis.SetLogger(silentRedisLogger{}) }

// InitRedis 按配置建立 Redis 连接：enabled=false（或 cfg 为 nil）则断开既有连接、转回本地缓存。
// 连接成功后把所有已注册缓存的内存数据迁移进 Redis。REDIS 为 startup-only，仅启动时调用一次。
func InitRedis(cfg *config.RedisConfig) error {
	if cfg == nil || !cfg.Enabled {
		CloseRedis()
		return nil
	}
	host := cfg.Host
	if host == "" {
		host = "127.0.0.1"
	}
	port := cfg.Port
	if port == 0 {
		port = 6379
	}
	client := redis.NewClient(&redis.Options{
		Addr:       fmt.Sprintf("%s:%d", host, port),
		Password:   cfg.Password,
		DB:         cfg.DB,
		MaxRetries: 3,
	})
	ctx, cancel := context.WithTimeout(context.Background(), redisOpTimeout)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return err
	}

	redisMu.Lock()
	old := redisClient
	redisClient = client
	redisMu.Unlock()
	if old != nil {
		_ = old.Close()
	}

	migrateAllToRedis()
	return nil
}

// CloseRedis 断开 Redis 连接并转回本地缓存（幂等）。
func CloseRedis() {
	redisMu.Lock()
	c := redisClient
	redisClient = nil
	redisMu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

// RedisEnabled 报告当前是否使用 Redis 后端。
func RedisEnabled() bool { return getRedis() != nil }

// RedisClient 返回共享的 go-redis 客户端；未启用时返回 nil。
// 调用方可在 client 上直接使用 ZADD / ZRANGE / ZREVRANGE 等 sorted-set 命令。
func RedisClient() *redis.Client { return getRedis() }

func getRedis() *redis.Client {
	redisMu.RLock()
	defer redisMu.RUnlock()
	return redisClient
}

func redisCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), redisOpTimeout)
}

// registerMigrator 登记一个缓存的迁移回调（Cache 构造时调用）。
func registerMigrator(fn func()) {
	migratorsMu.Lock()
	migrators = append(migrators, fn)
	migratorsMu.Unlock()
}

func migrateAllToRedis() {
	migratorsMu.Lock()
	fns := append([]func(){}, migrators...)
	migratorsMu.Unlock()
	for _, fn := range fns {
		fn()
	}
}
