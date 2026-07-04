package stats

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
)

func TestOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='matches'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("matches table not found")
	}
}

func TestRecordMatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	results := map[int]config.RecordData{
		1001: {ID: 1, Player: 1001, Chart: intPtr(42), Score: 950000, Accuracy: 0.985, Perfect: 800, Good: 50, Bad: 0, Miss: 0, MaxCombo: 850, FullCombo: true},
		1002: {ID: 2, Player: 1002, Chart: intPtr(42), Score: 880000, Accuracy: 0.920, Perfect: 700, Good: 100, Bad: 30, Miss: 20, MaxCombo: 400},
		1003: {ID: 3, Player: 1003, Chart: intPtr(42), Score: 0, Accuracy: 0.750, Perfect: 300, Good: 200, Bad: 100, Miss: 250, MaxCombo: 100},
	}
	userIDs := []int{1001, 1002, 1003}
	names := map[int]string{1001: "Alice", 1002: "Bob", 1003: "Carol"}

	mr, err := s.RecordMatch(context.Background(), "room-abc", 42, "Test Chart", userIDs, results, names, 120.5)
	if err != nil {
		t.Fatalf("RecordMatch: %v", err)
	}
	if len(mr) != 3 {
		t.Fatalf("expected 3 results, got %d", len(mr))
	}

	// Verify match row
	var n int
	var dur float64
	if err := s.db.QueryRow("SELECT n, duration_sec FROM matches WHERE room_id='room-abc'").Scan(&n, &dur); err != nil {
		t.Fatal(err)
	}
	if n != 3 || dur != 120.5 {
		t.Errorf("n=%d dur=%f", n, dur)
	}

	// Verify rank
	var rank int
	s.db.QueryRow("SELECT rank FROM match_results WHERE user_id=1001").Scan(&rank)
	if rank != 1 {
		t.Errorf("rank for 1001 = %d, want 1", rank)
	}

	// Verify names in users table
	for uid, want := range names {
		var name string
		s.db.QueryRow("SELECT name FROM users WHERE id=?", uid).Scan(&name)
		if name != want {
			t.Errorf("user %d name=%q, want %q", uid, name, want)
		}
	}

	// Verify player_stats rollup
	var games, playTime int
	s.db.QueryRow("SELECT games, play_time_sec FROM player_stats WHERE user_id=1001").Scan(&games, &playTime)
	if games != 1 || playTime != 120 {
		t.Errorf("1001: games=%d playTime=%d", games, playTime)
	}

	// Verify chart_stats
	var plays int
	s.db.QueryRow("SELECT plays FROM chart_stats WHERE chart_id=42").Scan(&plays)
	if plays != 3 {
		t.Errorf("chart plays=%d, want 3", plays)
	}
}

func TestRecordMatchMultiple(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	r1 := map[int]config.RecordData{
		1001: {Player: 1001, Score: 100, Accuracy: 0.95},
		1002: {Player: 1002, Score: 90, Accuracy: 0.90},
	}
	s.RecordMatch(context.Background(), "r1", 1, "C1", []int{1001, 1002}, r1, nil, 60)

	r2 := map[int]config.RecordData{
		1001: {Player: 1001, Score: 200, Accuracy: 0.98},
		1002: {Player: 1002, Score: 180, Accuracy: 0.92},
	}
	s.RecordMatch(context.Background(), "r2", 1, "C1", []int{1001, 1002}, r2, nil, 90)

	var games, playTime int
	s.db.QueryRow("SELECT games, play_time_sec FROM player_stats WHERE user_id=1001").Scan(&games, &playTime)
	if games != 2 || playTime != 150 {
		t.Errorf("1001: games=%d playTime=%d", games, playTime)
	}

	var plays int
	s.db.QueryRow("SELECT plays FROM chart_stats WHERE chart_id=1").Scan(&plays)
	if plays != 4 {
		t.Errorf("chart plays=%d, want 4", plays)
	}
}

func TestELORating(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// Game 1: 1001 beats 1002
	r := map[int]config.RecordData{
		1001: {Player: 1001, Score: 100, Accuracy: 0.9},
		1002: {Player: 1002, Score: 90, Accuracy: 0.8},
	}
	mr, err := s.RecordMatch(context.Background(), "r1", 0, "", []int{1001, 1002}, r, nil, 30)
	if err != nil {
		t.Fatalf("RecordMatch: %v", err)
	}
	// Winner gains rating, loser loses
	if mr[0].Rating <= 1500 {
		t.Errorf("winner rating should be > 1500, got %f", mr[0].Rating)
	}
	if mr[1].Rating >= 1500 {
		t.Errorf("loser rating should be < 1500, got %f", mr[1].Rating)
	}
	if mathAbs(mr[0].Rating-1500) < 0.01 {
		t.Error("rating should have changed from baseline")
	}
}

func TestPlayerProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	r := map[int]config.RecordData{
		1001: {Player: 1001, Score: 900000, Accuracy: 0.98},
	}
	s.RecordMatch(context.Background(), "r1", 0, "", []int{1001}, r, map[int]string{1001: "Alice"}, 45)

	p, err := s.GetPlayerProfile(1001)
	if err != nil {
		t.Fatalf("GetPlayerProfile: %v", err)
	}
	if p.Name != "Alice" {
		t.Errorf("name=%q, want Alice", p.Name)
	}
	if p.Games != 1 || p.PlayTimeSec != 45 || p.TotalScore != 900000 {
		t.Errorf("games=%d pt=%d score=%d", p.Games, p.PlayTimeSec, p.TotalScore)
	}
}

func TestLeaderboard(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	s.RecordMatch(context.Background(), "r1", 0, "", []int{1001}, map[int]config.RecordData{1001: {Player: 1001, Score: 100, Accuracy: 0.9}}, nil, 30)
	s.RecordMatch(context.Background(), "r2", 0, "", []int{1002}, map[int]config.RecordData{1002: {Player: 1002, Score: 200, Accuracy: 0.95}}, nil, 60)
	s.RecordMatch(context.Background(), "r3", 0, "", []int{1002}, map[int]config.RecordData{1002: {Player: 1002, Score: 150, Accuracy: 0.9}}, nil, 40)

	lb, err := s.GetLeaderboardByPlayTime(10)
	if err != nil {
		t.Fatalf("GetLeaderboardByPlayTime: %v", err)
	}
	if len(lb) < 2 || lb[0].UserID != 1002 || lb[0].PlayTimeSec != 100 {
		t.Errorf("top playtime: id=%d pt=%d", lb[0].UserID, lb[0].PlayTimeSec)
	}
}

func TestRecentMatches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	s.RecordMatch(context.Background(), "r1", 42, "Alpha", []int{1001}, map[int]config.RecordData{1001: {Player: 1001, Score: 100, Accuracy: 0.9}}, nil, 30)
	time.Sleep(1 * time.Second)
	s.RecordMatch(context.Background(), "r2", 43, "Beta", []int{1001}, map[int]config.RecordData{1001: {Player: 1001, Score: 200, Accuracy: 0.95}}, nil, 45)
	time.Sleep(1 * time.Second)
	s.RecordMatch(context.Background(), "r3", 42, "Alpha", []int{1002}, map[int]config.RecordData{1002: {Player: 1002, Score: 300, Accuracy: 0.99}}, nil, 60)

	recent, err := s.GetRecentMatches(1001, 10)
	if err != nil {
		t.Fatalf("GetRecentMatches: %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("expected 2 recent matches for 1001, got %d", len(recent))
	}
	// Most recent first
	if recent[0].ChartName != "Beta" {
		t.Errorf("most recent should be Beta, got %s", recent[0].ChartName)
	}
	if recent[1].ChartName != "Alpha" {
		t.Errorf("older should be Alpha, got %s", recent[1].ChartName)
	}
}

func TestChartPopularity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	s.RecordMatch(context.Background(), "r1", 42, "Hot Chart", []int{1001}, map[int]config.RecordData{1001: {Player: 1001, Score: 100, Accuracy: 0.9}}, nil, 30)
	s.RecordMatch(context.Background(), "r2", 42, "Hot Chart", []int{1001}, map[int]config.RecordData{1001: {Player: 1001, Score: 200, Accuracy: 0.95}}, nil, 45)

	cp, err := s.GetChartStats(42)
	if err != nil {
		t.Fatalf("GetChartStats: %v", err)
	}
	if cp.Plays != 2 || cp.Popularity <= 0 || cp.LastPlayedAt == "" {
		t.Errorf("plays=%d pop=%f last=%s", cp.Plays, cp.Popularity, cp.LastPlayedAt)
	}

	list, err := s.GetChartPopularity(10)
	if err != nil || len(list) < 1 {
		t.Fatal("GetChartPopularity empty")
	}
}

func TestCleanupDetail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	s.RecordMatch(context.Background(), "r1", 0, "", []int{1001}, map[int]config.RecordData{1001: {Player: 1001, Score: 100, Accuracy: 0.95}}, nil, 30)

	// 0 = keep forever
	s.CleanupDetail(0)
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM match_results").Scan(&count)
	if count != 1 {
		t.Errorf("cleanup(0) should keep all, got %d", count)
	}

	// Age and clean
	s.db.Exec("UPDATE matches SET started_at = datetime('now', '-100 days')")
	s.CleanupDetail(1)
	s.db.QueryRow("SELECT COUNT(*) FROM match_results").Scan(&count)
	if count != 0 {
		t.Errorf("cleanup(1) on aged: got %d, want 0", count)
	}

	var games int
	s.db.QueryRow("SELECT games FROM player_stats WHERE user_id=1001").Scan(&games)
	if games != 1 {
		t.Error("player_stats should survive cleanup")
	}
}

func TestOpenEmptyPath(t *testing.T) {
	_, err := Open("")
	if err == nil {
		t.Error("expected error for empty path")
	}
}

// TestRecordMatchEmptyNameDoesNotOverwrite 验证 userNames 缺失（玩家离线）时，
// users.name 不会被空串覆盖已有名字。
func TestRecordMatchEmptyNameDoesNotOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// 第一次：写入玩家 1001 的名字
	r1 := map[int]config.RecordData{1001: {Player: 1001, Score: 100, Accuracy: 0.9}}
	s.RecordMatch(context.Background(), "r1", 0, "", []int{1001}, r1, map[int]string{1001: "Alice"}, 30)

	var name string
	s.db.QueryRow("SELECT name FROM users WHERE id=1001").Scan(&name)
	if name != "Alice" {
		t.Fatalf("first match: name=%q, want Alice", name)
	}

	// 第二次：玩家已离线，userNames 不含 1001（name 为空串）
	r2 := map[int]config.RecordData{1001: {Player: 1001, Score: 200, Accuracy: 0.95}}
	s.RecordMatch(context.Background(), "r2", 0, "", []int{1001}, r2, nil, 45)

	s.db.QueryRow("SELECT name FROM users WHERE id=1001").Scan(&name)
	if name != "Alice" {
		t.Errorf("empty name overwrote: got %q, want Alice", name)
	}
}

func TestMissingProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	_, err = s.GetPlayerProfile(9999)
	if err == nil {
		t.Error("expected error for missing player")
	}
}

// ---------- RecordMatch 边界 ----------

// TestRecordMatch_EmptyUsers 验证空用户列表不报错且不插入 match_results。
func TestRecordMatch_EmptyUsers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	mr, err := s.RecordMatch(context.Background(), "room-empty", 42, "Test", []int{}, map[int]config.RecordData{}, nil, 0)
	if err != nil {
		t.Fatalf("RecordMatch with empty users should not error, got %v", err)
	}
	if len(mr) != 0 {
		t.Errorf("expected 0 results, got %d", len(mr))
	}
	// match 行仍应存在（n=0）
	var n int
	if err := s.db.QueryRow("SELECT n FROM matches WHERE room_id='room-empty'").Scan(&n); err != nil {
		t.Fatalf("match row should exist: %v", err)
	}
	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
	// match_results 应为空
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM match_results WHERE match_id IN (SELECT id FROM matches WHERE room_id='room-empty')").Scan(&count)
	if count != 0 {
		t.Errorf("match_results count = %d, want 0", count)
	}
}

// TestRecordMatch_SinglePlayerELOUnchanged 验证单人房时 ELO 不变（无配对计算）。
func TestRecordMatch_SinglePlayerELOUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	uid := 5001
	results := map[int]config.RecordData{
		uid: {ID: 1, Player: uid, Score: 900000, Accuracy: 0.95},
	}
	mr, err := s.RecordMatch(context.Background(), "room-single", 0, "", []int{uid}, results, map[int]string{uid: "Solo"}, 60)
	if err != nil {
		t.Fatalf("RecordMatch: %v", err)
	}
	if len(mr) != 1 {
		t.Fatalf("expected 1 result, got %d", len(mr))
	}
	// 单人时 ELO 应保持基础值（1500），无配对增量
	if mr[0].Rating != eloBaseRating {
		t.Errorf("single player ELO should stay at base %v, got %v", eloBaseRating, mr[0].Rating)
	}
	// rank 应为 1
	var rank int
	s.db.QueryRow("SELECT rank FROM match_results WHERE user_id=?", uid).Scan(&rank)
	if rank != 1 {
		t.Errorf("single player rank = %d, want 1", rank)
	}
}

// TestRecordMatch_TiedScoresSameRank 验证同分数的玩家共享相同 rank。
func TestRecordMatch_TiedScoresSameRank(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// 三个玩家同分数 → 都应是 rank=1
	uid1, uid2, uid3 := 6001, 6002, 6003
	results := map[int]config.RecordData{
		uid1: {ID: 1, Player: uid1, Score: 500000, Accuracy: 0.9},
		uid2: {ID: 2, Player: uid2, Score: 500000, Accuracy: 0.9},
		uid3: {ID: 3, Player: uid3, Score: 500000, Accuracy: 0.9},
	}
	userIDs := []int{uid1, uid2, uid3}
	names := map[int]string{uid1: "A", uid2: "B", uid3: "C"}

	if _, err := s.RecordMatch(context.Background(), "room-tied", 0, "", userIDs, results, names, 30); err != nil {
		t.Fatalf("RecordMatch: %v", err)
	}
	for _, uid := range userIDs {
		var rank int
		if err := s.db.QueryRow("SELECT rank FROM match_results WHERE user_id=?", uid).Scan(&rank); err != nil {
			t.Fatalf("query rank for %d: %v", uid, err)
		}
		if rank != 1 {
			t.Errorf("tied player %d rank = %d, want 1", uid, rank)
		}
	}
}

// TestRecordMatch_NilUserNamesDoesNotPanic 验证 userNames 为 nil map 不 panic。
func TestRecordMatch_NilUserNamesDoesNotPanic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	uid := 7001
	results := map[int]config.RecordData{
		uid: {ID: 1, Player: uid, Score: 100, Accuracy: 0.5},
	}
	defer func() {
		if rec := recover(); rec != nil {
			t.Errorf("RecordMatch with nil userNames should not panic, got %v", rec)
		}
	}()
	if _, err := s.RecordMatch(context.Background(), "room-nilnames", 0, "", []int{uid}, results, nil, 10); err != nil {
		t.Fatalf("RecordMatch: %v", err)
	}
	// users.name 应为空字符串（首次写入，nil map 返回零值）
	var name string
	s.db.QueryRow("SELECT name FROM users WHERE id=?", uid).Scan(&name)
	if name != "" {
		t.Errorf("nil userNames should write empty name, got %q", name)
	}
}

// TestRecordMatch_RepeatedMatchAccumulatesGames 验证同一玩家多次对局 games 累加。
func TestRecordMatch_RepeatedMatchAccumulatesGames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	uid := 8001
	results := map[int]config.RecordData{
		uid: {ID: 1, Player: uid, Score: 100, Accuracy: 0.5},
	}
	// 连续记录 3 局
	for i := 0; i < 3; i++ {
		if _, err := s.RecordMatch(context.Background(), "room-rep", 0, "", []int{uid}, results, map[int]string{uid: "Rep"}, 20); err != nil {
			t.Fatalf("RecordMatch iter %d: %v", i, err)
		}
	}
	var games int
	s.db.QueryRow("SELECT games FROM player_stats WHERE user_id=?", uid).Scan(&games)
	if games != 3 {
		t.Errorf("after 3 matches, games = %d, want 3", games)
	}
	// 总游戏时间应累加为 60
	var playTime int
	s.db.QueryRow("SELECT play_time_sec FROM player_stats WHERE user_id=?", uid).Scan(&playTime)
	if playTime != 60 {
		t.Errorf("after 3 matches * 20s, play_time_sec = %d, want 60", playTime)
	}
}

// TestRecordMatch_DurationZero 验证 durationSec=0 不报错。
func TestRecordMatch_DurationZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	uid := 9001
	results := map[int]config.RecordData{
		uid: {ID: 1, Player: uid, Score: 100, Accuracy: 0.5},
	}
	if _, err := s.RecordMatch(context.Background(), "room-zero-dur", 0, "", []int{uid}, results, map[int]string{uid: "Z"}, 0); err != nil {
		t.Fatalf("RecordMatch with duration=0 should not error: %v", err)
	}
	var playTime int
	s.db.QueryRow("SELECT play_time_sec FROM player_stats WHERE user_id=?", uid).Scan(&playTime)
	if playTime != 0 {
		t.Errorf("duration=0 → play_time_sec = %d, want 0", playTime)
	}
}

func intPtr(i int) *int { return &i }
func mathAbs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
