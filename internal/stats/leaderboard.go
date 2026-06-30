package stats

import (
	"context"
	"fmt"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/cache"
	"github.com/redis/go-redis/v9"
)

const (
	leaderboardKeyRating    = "stats:lb:rating"
	leaderboardKeyPlayTime  = "stats:lb:playtime"
	leaderboardKeyScore     = "stats:lb:score"
	chartHotKey             = "stats:chart:hot"
	redisOpTimeout          = 3 * time.Second
)

// SyncLeaderboard 在结算后将玩家的 rating / play_time_sec / total_score 同步到 Redis ZSET。
// Redis 未启用时 no-op。
func SyncLeaderboard(userID int, rating float64, playTimeSec int, totalScore int) {
	rdb := cache.RedisClient()
	if rdb == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), redisOpTimeout)
	defer cancel()

	pipe := rdb.Pipeline()
	pipe.ZAdd(ctx, leaderboardKeyRating, redis.Z{Score: rating, Member: userID})
	pipe.ZAdd(ctx, leaderboardKeyPlayTime, redis.Z{Score: float64(playTimeSec), Member: userID})
	pipe.ZAdd(ctx, leaderboardKeyScore, redis.Z{Score: float64(totalScore), Member: userID})
	if _, err := pipe.Exec(ctx); err != nil {
		// 排行榜同步失败不影响主流程，仅静默跳过。
		_ = err
	}
}

// SyncChartHot 将谱面热度分数同步到 Redis ZSET。
func SyncChartHot(chartID int, popularity float64) {
	rdb := cache.RedisClient()
	if rdb == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), redisOpTimeout)
	defer cancel()

	rdb.ZAdd(ctx, chartHotKey, redis.Z{Score: popularity, Member: chartID})
}

// LeaderboardEntry 是 Redis 排行榜中的条目。
type LeaderboardEntry struct {
	Rank   int
	UserID int
	Score  float64
}

// GetRedisLeaderboard 从 Redis ZSET 读取排行榜前 N 名（desc）。
// Redis 未启用时返回空切片。
func GetRedisLeaderboard(key string, limit int) ([]LeaderboardEntry, error) {
	rdb := cache.RedisClient()
	if rdb == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), redisOpTimeout)
	defer cancel()

	results, err := rdb.ZRevRangeWithScores(ctx, key, 0, int64(limit-1)).Result()
	if err != nil {
		return nil, fmt.Errorf("stats: redis leaderboard %s: %w", key, err)
	}
	out := make([]LeaderboardEntry, 0, len(results))
	for i, z := range results {
		uid, ok := z.Member.(string)
		if !ok {
			continue
		}
		// member 是 userID（int），ZADD 时会被序列化为字符串。
		out = append(out, LeaderboardEntry{Rank: i + 1, UserID: parseUserID(uid), Score: z.Score})
	}
	return out, nil
}

func parseUserID(s string) int {
	var id int
	fmt.Sscanf(s, "%d", &id)
	return id
}
