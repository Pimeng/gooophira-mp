package stats

import (
	"database/sql"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
	_ "modernc.org/sqlite" // database/sql driver
)

// popularityHalfLifeDays 是谱面热度半衰期（天）。
const popularityHalfLifeDays = 30

// Store 是 SQLite 持久化封装。db 并发安全（连接池），多 goroutine 同时写安全。
type Store struct {
	db *sql.DB
}

// Open 打开（或创建）SQLite 数据库，开启 WAL 模式、建表并执行迁移。
// path 为空时返回错误。
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("stats: db path is empty")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("stats: open: %w", err)
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("stats: wal: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("stats: foreign_keys: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("stats: schema: %w", err)
	}
	// 增量迁移：为老 DB 补加新列（列已存在时静默跳过）。
	for _, m := range migrations {
		db.Exec(m)
	}
	return &Store{db: db}, nil
}

// Close 关闭数据库连接。
func (s *Store) Close() error { return s.db.Close() }

// RecordMatch 在一笔事务中写入 match + match_results，并增量更新
// player_stats（含 play_time_sec / total_score）与 chart_stats（含 popularity）rollup 表。
//
// duration 为本局墙钟时长（秒）；results 的 key 为 userID；userIDs 保持房间成员顺序。
func (s *Store) RecordMatch(roomID string, chartID int, chartName string,
	userIDs []int, results map[int]config.RecordData, durationSec float64) error {

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("stats: begin tx: %w", err)
	}
	defer tx.Rollback()

	// 1. 插入 match 行
	res, err := tx.Exec(
		`INSERT INTO matches(room_id, chart_id, chart_name, duration_sec, n) VALUES(?,?,?,?,?)`,
		roomID, chartID, chartName, durationSec, len(userIDs),
	)
	if err != nil {
		return fmt.Errorf("stats: insert match: %w", err)
	}
	matchID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("stats: last insert id: %w", err)
	}

	// 2. 按 score desc 计算排名（同分同名）
	ranked := rankByScore(userIDs, results)

	// 3. 逐人插入 match_results + 更新 player_stats
	now := time.Now().UTC().Format(time.RFC3339)
	for _, rr := range ranked {
		rd := rr.record
		fc := 0
		if rd.FullCombo {
			fc = 1
		}
		if _, err := tx.Exec(
			`INSERT INTO match_results(match_id,user_id,score,accuracy,perfect,good,bad,miss,max_combo,full_combo,std,std_score,rank)
			 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			matchID, rd.Player, rd.Score, rd.Accuracy,
			rd.Perfect, rd.Good, rd.Bad, rd.Miss,
			rd.MaxCombo, fc, rd.Std, rd.StdScore, rr.rank,
		); err != nil {
			return fmt.Errorf("stats: insert match_result user=%d: %w", rd.Player, err)
		}

		// 增量更新 player_stats（upsert）：games / wins / sum_acc / best_score / total_score / play_time_sec
		if _, err := tx.Exec(
			`INSERT INTO player_stats(user_id,games,wins,sum_acc,best_score,total_score,play_time_sec,updated_at)
			 VALUES(?,1,?,?,?,?,?,?)
			 ON CONFLICT(user_id) DO UPDATE SET
			   games         = games + 1,
			   wins          = wins   + excluded.wins,
			   sum_acc       = sum_acc + excluded.sum_acc,
			   best_score    = MAX(best_score, excluded.best_score),
			   total_score   = total_score + excluded.total_score,
			   play_time_sec = play_time_sec + excluded.play_time_sec,
			   updated_at    = excluded.updated_at`,
			rd.Player, boolToInt(rr.rank == 1), rd.Accuracy, rd.Score, rd.Score, int(durationSec), now,
		); err != nil {
			return fmt.Errorf("stats: upsert player_stats user=%d: %w", rd.Player, err)
		}

		// 更新 users 名字缓存
		if _, err := tx.Exec(`INSERT OR IGNORE INTO users(id,name,last_seen) VALUES(?,?,?)`, rd.Player, "", now); err != nil {
			return fmt.Errorf("stats: upsert user %d: %w", rd.Player, err)
		}
	}

	// 4. 更新 chart_stats（含 recency 加权 popularity）
	if chartID != 0 {
		n := len(results)
		passCount := 0
		for _, rd := range results {
			if rd.Score > 0 {
				passCount++
			}
		}
		initialPassRate := 0.0
		if n > 0 {
			initialPassRate = float64(passCount) / float64(n)
		}
		// popularity: 旧值按 last_played_at 衰减后 + 本次游玩次数。
		// 衰减公式: old * exp(-ln(2) * days_since_last / halfLifeDays) + n
		// exp() 参数 = -ln(2)/halfLifeDays ≈ 预计算常量传入。
		decayExpr := `CASE
			WHEN last_played_at != '' THEN popularity * EXP((julianday(?) - julianday(last_played_at)) * ?) + ?
			ELSE ?
		END`
		decayLambda := -math.Ln2 / float64(popularityHalfLifeDays)
		if _, err := tx.Exec(
			fmt.Sprintf(
				`INSERT INTO chart_stats(chart_id,chart_name,plays,sum_acc,pass_rate,last_played_at,popularity,updated_at)
				 VALUES(?,?,?,?,?,?,?,?)
				 ON CONFLICT(chart_id) DO UPDATE SET
				   chart_name     = excluded.chart_name,
				   plays          = plays + excluded.plays,
				   sum_acc        = sum_acc + excluded.sum_acc,
				   pass_rate      = CASE WHEN plays + excluded.plays > 0
				                     THEN CAST((pass_rate * plays + ?) AS REAL) / (plays + excluded.plays)
				                     ELSE 0 END,
				   last_played_at = excluded.last_played_at,
				   popularity     = %s,
				   updated_at     = excluded.updated_at`, decayExpr),
			chartID, chartName, n, sumAcc(results), initialPassRate, now, float64(n), now,
			float64(passCount),
			// decayExpr args:
			now, decayLambda, float64(n), float64(n),
		); err != nil {
			return fmt.Errorf("stats: upsert chart_stats chart=%d: %w", chartID, err)
		}
	}

	return tx.Commit()
}

// ---------- 查询 API（只读 rollup 表，永不扫历史明细）----------

// PlayerProfile 是玩家档案页数据。
type PlayerProfile struct {
	UserID      int
	Games       int
	Wins        int
	AvgAcc      float64
	BestScore   int
	TotalScore  int64
	PlayTimeSec int
	Rating      float64
	UpdatedAt   string
	sumAcc      float64 // 内部使用，导出 AvgAcc
}

// GetPlayerProfile 从 player_stats 读取玩家终身聚合。
func (s *Store) GetPlayerProfile(userID int) (*PlayerProfile, error) {
	row := s.db.QueryRow(
		`SELECT user_id, games, wins, sum_acc, best_score, total_score, play_time_sec, rating, updated_at
		 FROM player_stats WHERE user_id = ?`, userID,
	)
	p := &PlayerProfile{}
	if err := row.Scan(&p.UserID, &p.Games, &p.Wins, &p.sumAcc, &p.BestScore,
		&p.TotalScore, &p.PlayTimeSec, &p.Rating, &p.UpdatedAt); err != nil {
		return nil, fmt.Errorf("stats: player %d: %w", userID, err)
	}
	p.AvgAcc = avgFromSum(p.sumAcc, p.Games)
	return p, nil
}

// ChartPopularity 是谱面热度项。
type ChartPopularity struct {
	ChartID      int
	ChartName    string
	Plays        int
	AvgAcc       float64
	PassRate     float64
	LastPlayedAt string
	Popularity   float64
	sumAcc       float64 // 内部使用，导出 AvgAcc
}

// GetChartPopularity 按 popularity desc 返回排行榜前 N 名。
func (s *Store) GetChartPopularity(limit int) ([]ChartPopularity, error) {
	rows, err := s.db.Query(
		`SELECT chart_id, chart_name, plays, sum_acc, pass_rate, last_played_at, popularity
		 FROM chart_stats WHERE plays > 0
		 ORDER BY popularity DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("stats: chart popularity: %w", err)
	}
	defer rows.Close()
	return scanChartPopularity(rows)
}

// GetChartStats 获取单个谱面的聚合统计。
func (s *Store) GetChartStats(chartID int) (*ChartPopularity, error) {
	row := s.db.QueryRow(
		`SELECT chart_id, chart_name, plays, sum_acc, pass_rate, last_played_at, popularity
		 FROM chart_stats WHERE chart_id = ?`, chartID,
	)
	c := &ChartPopularity{}
	if err := row.Scan(&c.ChartID, &c.ChartName, &c.Plays, &c.sumAcc, &c.PassRate,
		&c.LastPlayedAt, &c.Popularity); err != nil {
		return nil, fmt.Errorf("stats: chart %d: %w", chartID, err)
	}
	c.AvgAcc = avgFromSum(c.sumAcc, c.Plays)
	return c, nil
}

// PlayerLeaderboard 是排行榜条目。
type PlayerLeaderboard struct {
	Rank        int
	UserID      int
	Games       int
	Wins        int
	AvgAcc      float64
	BestScore   int
	TotalScore  int64
	PlayTimeSec int
	Rating      float64
}

// GetLeaderboardByRating 按 rating desc 返回前 N 名。
func (s *Store) GetLeaderboardByRating(limit int) ([]PlayerLeaderboard, error) {
	rows, err := s.db.Query(
		`SELECT user_id, games, wins, sum_acc, best_score, total_score, play_time_sec, rating
		 FROM player_stats WHERE games > 0
		 ORDER BY rating DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("stats: leaderboard rating: %w", err)
	}
	defer rows.Close()
	return scanLeaderboard(rows)
}

// GetLeaderboardByPlayTime 按 play_time_sec desc 返回前 N 名。
func (s *Store) GetLeaderboardByPlayTime(limit int) ([]PlayerLeaderboard, error) {
	rows, err := s.db.Query(
		`SELECT user_id, games, wins, sum_acc, best_score, total_score, play_time_sec, rating
		 FROM player_stats WHERE games > 0
		 ORDER BY play_time_sec DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("stats: leaderboard playtime: %w", err)
	}
	defer rows.Close()
	return scanLeaderboard(rows)
}

// GetLeaderboardByTotalScore 按 total_score desc 返回前 N 名。
func (s *Store) GetLeaderboardByTotalScore(limit int) ([]PlayerLeaderboard, error) {
	rows, err := s.db.Query(
		`SELECT user_id, games, wins, sum_acc, best_score, total_score, play_time_sec, rating
		 FROM player_stats WHERE games > 0
		 ORDER BY total_score DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("stats: leaderboard score: %w", err)
	}
	defer rows.Close()
	return scanLeaderboard(rows)
}

// ---------- helpers ----------

type rankedResult struct {
	record config.RecordData
	rank   int
}

func rankByScore(userIDs []int, results map[int]config.RecordData) []rankedResult {
	type entry struct {
		userID int
		score  int
	}
	entries := make([]entry, 0, len(userIDs))
	for _, uid := range userIDs {
		if rd, ok := results[uid]; ok {
			entries = append(entries, entry{uid, rd.Score})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].score > entries[j].score })

	ranked := make([]rankedResult, 0, len(entries))
	for i, e := range entries {
		rank := i + 1
		if i > 0 && e.score == entries[i-1].score {
			rank = ranked[i-1].rank
		}
		ranked = append(ranked, rankedResult{record: results[e.userID], rank: rank})
	}
	return ranked
}

func sumAcc(results map[int]config.RecordData) float64 {
	var s float64
	for _, rd := range results {
		s += rd.Accuracy
	}
	return s
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func avgFromSum(sum float64, count int) float64 {
	if count == 0 {
		return 0
	}
	return math.Round(sum/float64(count)*10000) / 10000
}

func scanLeaderboard(rows *sql.Rows) ([]PlayerLeaderboard, error) {
	var out []PlayerLeaderboard
	rank := 0
	for rows.Next() {
		rank++
		e := PlayerLeaderboard{Rank: rank}
		var sumAcc float64
		if err := rows.Scan(&e.UserID, &e.Games, &e.Wins, &sumAcc, &e.BestScore,
			&e.TotalScore, &e.PlayTimeSec, &e.Rating); err != nil {
			return nil, err
		}
		e.AvgAcc = avgFromSum(sumAcc, e.Games)
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanChartPopularity(rows *sql.Rows) ([]ChartPopularity, error) {
	var out []ChartPopularity
	for rows.Next() {
		e := ChartPopularity{}
		var sumAcc float64
		if err := rows.Scan(&e.ChartID, &e.ChartName, &e.Plays, &sumAcc, &e.PassRate,
			&e.LastPlayedAt, &e.Popularity); err != nil {
			return nil, err
		}
		e.AvgAcc = avgFromSum(sumAcc, e.Plays)
		out = append(out, e)
	}
	return out, rows.Err()
}
