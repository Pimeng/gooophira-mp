package stats

import (
	"database/sql"
	"fmt"
	"sort"

	"github.com/Pimeng/gooophira-mp/internal/config"
	_ "modernc.org/sqlite" // database/sql driver
)

// Store 是 SQLite 持久化封装。db 并发安全（连接池），多 goroutine 同时写安全。
type Store struct {
	db *sql.DB
}

// Open 打开（或创建）SQLite 数据库，开启 WAL 模式并建表。path 为空时返回错误。
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("stats: db path is empty")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("stats: open: %w", err)
	}
	// WAL 模式：读写不互斥，HTTP 读与结算写可并发。
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
	return &Store{db: db}, nil
}

// Close 关闭数据库连接。
func (s *Store) Close() error { return s.db.Close() }

// RecordMatch 在一笔事务中写入 match + match_results，并增量更新
// player_stats 与 chart_stats rollup 表。
//
// results 的 key 为 userID；userIDs 保持房间成员顺序。
func (s *Store) RecordMatch(roomID string, chartID int, chartName string,
	userIDs []int, results map[int]config.RecordData) error {

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("stats: begin tx: %w", err)
	}
	defer tx.Rollback() // Commit 后为 no-op

	// 1. 插入 match 行
	res, err := tx.Exec(
		`INSERT INTO matches(room_id, chart_id, chart_name, n) VALUES(?,?,?,?)`,
		roomID, chartID, chartName, len(userIDs),
	)
	if err != nil {
		return fmt.Errorf("stats: insert match: %w", err)
	}
	matchID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("stats: last insert id: %w", err)
	}

	// 2. 按 score desc 计算排名（同分同名——本局内简单即可）
	ranked := rankByScore(userIDs, results)

	// 3. 逐人插入 match_results + 更新 player_stats
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

		// 增量更新 player_stats（upsert）
		if _, err := tx.Exec(
			`INSERT INTO player_stats(user_id,games,wins,sum_acc,best_score,updated_at)
			 VALUES(?,1,?,?,?,datetime('now'))
			 ON CONFLICT(user_id) DO UPDATE SET
			   games      = games + 1,
			   wins       = wins   + excluded.wins,
			   sum_acc    = sum_acc + excluded.sum_acc,
			   best_score = MAX(best_score, excluded.best_score),
			   updated_at = excluded.updated_at`,
			rd.Player, boolToInt(rr.rank == 1), rd.Accuracy, rd.Score,
		); err != nil {
			return fmt.Errorf("stats: upsert player_stats user=%d: %w", rd.Player, err)
		}

		// 更新 users 名字缓存（惰性 upsert）
		// name 暂不可用（结算时无用户名），留空；后续可通过 Phira API 补填。
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO users(id,name) VALUES(?,?)`,
			rd.Player, "",
		); err != nil {
			return fmt.Errorf("stats: upsert user %d: %w", rd.Player, err)
		}
	}

	// 4. 更新 chart_stats
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
		if _, err := tx.Exec(
			`INSERT INTO chart_stats(chart_id,chart_name,plays,sum_acc,pass_rate,updated_at)
			 VALUES(?,?,?,?,?,datetime('now'))
			 ON CONFLICT(chart_id) DO UPDATE SET
			   chart_name = excluded.chart_name,
			   plays      = plays + excluded.plays,
			   sum_acc    = sum_acc + excluded.sum_acc,
			   pass_rate  = CASE WHEN plays + excluded.plays > 0
			                 THEN CAST((pass_rate * plays + ?) AS REAL) / (plays + excluded.plays)
			                 ELSE 0 END,
			   updated_at = excluded.updated_at`,
			chartID, chartName, n, sumAcc(results), initialPassRate,
			float64(passCount),
		); err != nil {
			return fmt.Errorf("stats: upsert chart_stats chart=%d: %w", chartID, err)
		}
	}

	return tx.Commit()
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
	// 按 score desc 排序
	sort.Slice(entries, func(i, j int) bool { return entries[i].score > entries[j].score })

	ranked := make([]rankedResult, 0, len(entries))
	for i, e := range entries {
		rank := i + 1
		// 同分同名
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
