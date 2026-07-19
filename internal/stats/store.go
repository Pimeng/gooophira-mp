package stats

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
	_ "modernc.org/sqlite"
)

const (
	popularityHalfLifeDays = 30
	eloKFactor             = 32
	eloBaseRating          = 1500.0
)

// Store 是 SQLite 持久化封装。db 并发安全（连接池）。
type Store struct {
	db *sql.DB
}

// Open 打开（或创建）SQLite 数据库，开启 WAL 模式、建表并执行迁移。
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
	for _, m := range migrations {
		db.Exec(m)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// RecordMatchResult 是 RecordMatch 返回的每位玩家的更新后聚合值。
type RecordMatchResult struct {
	UserID      int
	Rating      float64
	PlayTimeSec int
	TotalScore  int
}

// RecordMatch 在一笔事务中写入 match + match_results + ELO rating 更新，
// 增量更新 player_stats（含 play_time_sec / total_score）与 chart_stats（含 popularity）。
//
// userNames 是 userID → 名字（来自服务器状态，可为空 map）。
// ctx 控制取消：服务器关闭时 ctx 取消会让进行中的 SQL 立即返回。
// 返回每位参与玩家的更新后 rating / play_time / total_score。
func (s *Store) RecordMatch(ctx context.Context, roomID string, chartID int, chartName string,
	userIDs []int, results map[int]config.RecordData, userNames map[int]string,
	durationSec float64) ([]RecordMatchResult, error) {
	return s.RecordMatchEvent(ctx, "", roomID, chartID, chartName, userIDs, results, userNames, durationSec)
}

// RecordMatchEvent 以幂等方式记录 Agent 事件。
// 事件 ID 占用和全部比赛/统计更新共用一个 SQLite 事务。
func (s *Store) RecordMatchEvent(ctx context.Context, eventID, roomID string, chartID int, chartName string,
	userIDs []int, results map[int]config.RecordData, userNames map[int]string,
	durationSec float64) ([]RecordMatchResult, error) {

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("stats: begin tx: %w", err)
	}
	defer tx.Rollback()
	if eventID != "" {
		claim, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO consumed_events(event_id) VALUES(?)`, eventID)
		if err != nil {
			return nil, fmt.Errorf("stats: claim event: %w", err)
		}
		rows, err := claim.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("stats: claim event rows: %w", err)
		}
		if rows == 0 {
			return nil, nil
		}
	}

	// 1. 插入 match 行
	res, err := tx.ExecContext(ctx,
		`INSERT INTO matches(room_id, chart_id, chart_name, duration_sec, n) VALUES(?,?,?,?,?)`,
		roomID, chartID, chartName, durationSec, len(userIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("stats: insert match: %w", err)
	}
	matchID, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("stats: last insert id: %w", err)
	}

	// 2. 读取当前 rating 用于 ELO 计算
	oldRatings := s.loadRatings(ctx, tx, userIDs)

	// 3. 按 score desc 计算排名 + pairwise ELO 增量
	ranked := rankByScore(userIDs, results)
	eloDeltas := computeELO(ranked, oldRatings)

	// 4. 逐人插入 match_results + 更新 player_stats + users
	now := time.Now().UTC().Format(time.RFC3339)
	var out []RecordMatchResult
	for _, rr := range ranked {
		rd := rr.record
		uid := rd.Player
		fc := 0
		if rd.FullCombo {
			fc = 1
		}
		// rd.Std / rd.StdScore 是 *float64（nil = 客户端未上报），DB 列 NOT NULL
		// DEFAULT 0.0，因此 nil 时退化为 0.0 入库，避免 NOT NULL 约束失败。
		stdVal := 0.0
		if rd.Std != nil {
			stdVal = *rd.Std
		}
		stdScoreVal := 0.0
		if rd.StdScore != nil {
			stdScoreVal = *rd.StdScore
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO match_results(match_id,user_id,score,accuracy,perfect,good,bad,miss,max_combo,full_combo,std,std_score,rank)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			matchID, uid, rd.Score, rd.Accuracy,
			rd.Perfect, rd.Good, rd.Bad, rd.Miss,
			rd.MaxCombo, fc, stdVal, stdScoreVal, rr.rank,
		); err != nil {
			return nil, fmt.Errorf("stats: insert match_result user=%d: %w", uid, err)
		}

		// player_stats upsert（含 ELO rating 增量）
		newRating := oldRatings[uid] + eloDeltas[uid]
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO player_stats(user_id,games,wins,sum_acc,best_score,total_score,play_time_sec,rating,updated_at)
			 VALUES(?,1,?,?,?,?,?,?,?)
			 ON CONFLICT(user_id) DO UPDATE SET
			   games         = games + 1,
			   wins          = wins   + excluded.wins,
			   sum_acc       = sum_acc + excluded.sum_acc,
			   best_score    = MAX(best_score, excluded.best_score),
			   total_score   = total_score + excluded.total_score,
			   play_time_sec = play_time_sec + excluded.play_time_sec,
			   rating        = excluded.rating,
			   updated_at    = excluded.updated_at`,
			uid, boolToInt(rr.rank == 1), rd.Accuracy, rd.Score, rd.Score, int(durationSec), newRating, now,
		); err != nil {
			return nil, fmt.Errorf("stats: upsert player_stats user=%d: %w", uid, err)
		}

		// users 名字缓存：空 name 不覆盖（玩家离线时 userNames 缺该条目）
		name := userNames[uid]
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO users(id,name,last_seen) VALUES(?,?,?)
			 ON CONFLICT(id) DO UPDATE SET name=COALESCE(NULLIF(excluded.name, ''), users.name), last_seen=excluded.last_seen`,
			uid, name, now,
		); err != nil {
			return nil, fmt.Errorf("stats: upsert user %d: %w", uid, err)
		}

		out = append(out, RecordMatchResult{
			UserID: uid, Rating: newRating,
			PlayTimeSec: 0, // 在事务外查询累加值
			TotalScore:  rd.Score,
		})
	}

	// 5. 更新 chart_stats
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
		decayExpr := `CASE
			WHEN last_played_at != '' THEN popularity * EXP((julianday(?) - julianday(last_played_at)) * ?) + ?
			ELSE ?
		END`
		decayLambda := -math.Ln2 / float64(popularityHalfLifeDays)
		if _, err := tx.ExecContext(ctx,
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
			now, decayLambda, float64(n), float64(n),
		); err != nil {
			return nil, fmt.Errorf("stats: upsert chart_stats chart=%d: %w", chartID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("stats: commit: %w", err)
	}

	// 事务提交后回查 play_time_sec 累加值（仅需一次批量查询）
	for i := range out {
		var pt int
		if err := s.db.QueryRowContext(ctx, "SELECT play_time_sec FROM player_stats WHERE user_id=?", out[i].UserID).Scan(&pt); err == nil {
			out[i].PlayTimeSec = pt
		}
	}
	return out, nil
}

// loadRatings 从 player_stats 读取当前 rating（新玩家默认 1500）。
func (s *Store) loadRatings(ctx context.Context, tx *sql.Tx, userIDs []int) map[int]float64 {
	ratings := make(map[int]float64, len(userIDs))
	for _, uid := range userIDs {
		ratings[uid] = eloBaseRating
	}
	// 批量读取已有记录
	rows, err := tx.QueryContext(ctx, `SELECT user_id, rating FROM player_stats WHERE user_id IN (`+placeholders(len(userIDs))+`)`, intsToAny(userIDs)...)
	if err != nil {
		return ratings
	}
	defer rows.Close()
	for rows.Next() {
		var uid int
		var r float64
		if rows.Scan(&uid, &r) == nil {
			ratings[uid] = r
		}
	}
	return ratings
}

// computeELO 对所有配对计算 ELO 增量。新玩家从 1500 起步。
func computeELO(ranked []rankedResult, oldRatings map[int]float64) map[int]float64 {
	deltas := make(map[int]float64)
	type entry struct {
		uid   int
		score int
		rank  int
	}
	entries := make([]entry, len(ranked))
	for i, rr := range ranked {
		entries[i] = entry{uid: rr.record.Player, score: rr.record.Score, rank: rr.rank}
	}
	n := len(entries)
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			a, b := entries[i], entries[j]
			ra := oldRatings[a.uid]
			rb := oldRatings[b.uid]
			// 预期胜率
			ea := 1.0 / (1.0 + math.Pow(10, (rb-ra)/400.0))
			eb := 1.0 - ea
			// 实际得分
			var sa, sb float64
			if a.rank < b.rank {
				sa, sb = 1, 0
			} else if a.rank > b.rank {
				sa, sb = 0, 1
			} else {
				sa, sb = 0.5, 0.5
			}
			deltas[a.uid] += eloKFactor * (sa - ea)
			deltas[b.uid] += eloKFactor * (sb - eb)
		}
	}
	return deltas
}

// ---------- 查询 API ----------

// PlayerProfile 是玩家档案页数据。
type PlayerProfile struct {
	UserID      int
	Name        string
	Games       int
	Wins        int
	AvgAcc      float64
	BestScore   int
	TotalScore  int64
	PlayTimeSec int
	Rating      float64
	UpdatedAt   string
	sumAcc      float64
}

func (s *Store) GetPlayerProfile(userID int) (*PlayerProfile, error) {
	row := s.db.QueryRow(
		`SELECT p.user_id, u.name, p.games, p.wins, p.sum_acc, p.best_score, p.total_score, p.play_time_sec, p.rating, p.updated_at
		 FROM player_stats p LEFT JOIN users u ON u.id = p.user_id
		 WHERE p.user_id = ?`, userID,
	)
	p := &PlayerProfile{}
	if err := row.Scan(&p.UserID, &p.Name, &p.Games, &p.Wins, &p.sumAcc, &p.BestScore,
		&p.TotalScore, &p.PlayTimeSec, &p.Rating, &p.UpdatedAt); err != nil {
		return nil, fmt.Errorf("stats: player %d: %w", userID, err)
	}
	p.AvgAcc = avgFromSum(p.sumAcc, p.Games)
	return p, nil
}

// PlayerLeaderboard 是排行榜条目。
type PlayerLeaderboard struct {
	Rank        int
	UserID      int
	Name        string
	Games       int
	Wins        int
	AvgAcc      float64
	BestScore   int
	TotalScore  int64
	PlayTimeSec int
	Rating      float64
}

func (s *Store) GetLeaderboardByRating(limit int) ([]PlayerLeaderboard, error) {
	rows, err := s.db.Query(
		`SELECT p.user_id, u.name, p.games, p.wins, p.sum_acc, p.best_score, p.total_score, p.play_time_sec, p.rating
		 FROM player_stats p LEFT JOIN users u ON u.id = p.user_id
		 WHERE p.games > 0 ORDER BY p.rating DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("stats: leaderboard rating: %w", err)
	}
	defer rows.Close()
	return scanLeaderboard(rows)
}

func (s *Store) GetLeaderboardByPlayTime(limit int) ([]PlayerLeaderboard, error) {
	rows, err := s.db.Query(
		`SELECT p.user_id, u.name, p.games, p.wins, p.sum_acc, p.best_score, p.total_score, p.play_time_sec, p.rating
		 FROM player_stats p LEFT JOIN users u ON u.id = p.user_id
		 WHERE p.games > 0 ORDER BY p.play_time_sec DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("stats: leaderboard playtime: %w", err)
	}
	defer rows.Close()
	return scanLeaderboard(rows)
}

func (s *Store) GetLeaderboardByTotalScore(limit int) ([]PlayerLeaderboard, error) {
	rows, err := s.db.Query(
		`SELECT p.user_id, u.name, p.games, p.wins, p.sum_acc, p.best_score, p.total_score, p.play_time_sec, p.rating
		 FROM player_stats p LEFT JOIN users u ON u.id = p.user_id
		 WHERE p.games > 0 ORDER BY p.total_score DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("stats: leaderboard score: %w", err)
	}
	defer rows.Close()
	return scanLeaderboard(rows)
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
	sumAcc       float64
}

func (s *Store) GetChartPopularity(limit int) ([]ChartPopularity, error) {
	rows, err := s.db.Query(
		`SELECT chart_id, chart_name, plays, sum_acc, pass_rate, last_played_at, popularity
		 FROM chart_stats WHERE plays > 0 ORDER BY popularity DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("stats: chart popularity: %w", err)
	}
	defer rows.Close()
	return scanChartPopularity(rows)
}

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

// RecentMatch 是玩家最近一场比赛的摘要。
type RecentMatch struct {
	MatchID   int64   `json:"match_id"`
	ChartID   int     `json:"chart_id"`
	ChartName string  `json:"chart_name"`
	Score     int     `json:"score"`
	Accuracy  float64 `json:"accuracy"`
	Rank      int     `json:"rank"`
	PlayedAt  string  `json:"played_at"`
}

// GetRecentMatches 返回玩家最近 N 场比赛。
func (s *Store) GetRecentMatches(userID, limit int) ([]RecentMatch, error) {
	rows, err := s.db.Query(
		`SELECT mr.match_id, m.chart_id, m.chart_name, mr.score, mr.accuracy, mr.rank, m.started_at
		 FROM match_results mr JOIN matches m ON m.id = mr.match_id
		 WHERE mr.user_id = ? ORDER BY m.started_at DESC LIMIT ?`, userID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("stats: recent matches %d: %w", userID, err)
	}
	defer rows.Close()
	var out []RecentMatch
	for rows.Next() {
		var m RecentMatch
		if err := rows.Scan(&m.MatchID, &m.ChartID, &m.ChartName, &m.Score, &m.Accuracy, &m.Rank, &m.PlayedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ---------- 辅助函数 ----------

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

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, 0, n*2-1)
	for i := 0; i < n; i++ {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '?')
	}
	return string(b)
}

func intsToAny(v []int) []any {
	out := make([]any, len(v))
	for i, x := range v {
		out[i] = x
	}
	return out
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
		if err := rows.Scan(&e.UserID, &e.Name, &e.Games, &e.Wins, &sumAcc, &e.BestScore,
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
