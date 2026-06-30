package stats

import (
	"path/filepath"
	"testing"

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
		1001: {
			ID: 1, Player: 1001, Chart: intPtr(42),
			Score: 950000, Accuracy: 0.985, Perfect: 800, Good: 50, Bad: 0, Miss: 0,
			MaxCombo: 850, FullCombo: true, Std: 0.0, StdScore: 0.0,
		},
		1002: {
			ID: 2, Player: 1002, Chart: intPtr(42),
			Score: 880000, Accuracy: 0.920, Perfect: 700, Good: 100, Bad: 30, Miss: 20,
			MaxCombo: 400, FullCombo: false, Std: 0.0, StdScore: 0.0,
		},
		1003: {
			ID: 3, Player: 1003, Chart: intPtr(42),
			Score: 0, Accuracy: 0.750, Perfect: 300, Good: 200, Bad: 100, Miss: 250,
			MaxCombo: 100, FullCombo: false, Std: 0.0, StdScore: 0.0,
		},
	}
	userIDs := []int{1001, 1002, 1003}

	if err := s.RecordMatch("room-abc", 42, "Test Chart", userIDs, results, 120.5); err != nil {
		t.Fatalf("RecordMatch: %v", err)
	}

	// Verify match row
	var n int
	var dur float64
	if err := s.db.QueryRow("SELECT n, duration_sec FROM matches WHERE room_id='room-abc'").Scan(&n, &dur); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("expected n=3, got %d", n)
	}
	if dur != 120.5 {
		t.Errorf("expected duration=120.5, got %f", dur)
	}

	// Verify match_results: 3 rows
	var mrCount int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM match_results").Scan(&mrCount); err != nil {
		t.Fatal(err)
	}
	if mrCount != 3 {
		t.Errorf("expected 3 match_results, got %d", mrCount)
	}

	// Verify rank
	var rank int
	if err := s.db.QueryRow("SELECT rank FROM match_results WHERE user_id=1001").Scan(&rank); err != nil {
		t.Fatal(err)
	}
	if rank != 1 {
		t.Errorf("expected rank=1 for user 1001, got %d", rank)
	}

	// Verify player_stats rollup
	for _, uid := range []int{1001, 1002, 1003} {
		var games, playTime int
		if err := s.db.QueryRow("SELECT games, play_time_sec FROM player_stats WHERE user_id=?", uid).Scan(&games, &playTime); err != nil {
			t.Errorf("player_stats for %d: %v", uid, err)
		}
		if games != 1 {
			t.Errorf("user %d: expected games=1, got %d", uid, games)
		}
		if playTime != 120 {
			t.Errorf("user %d: expected play_time_sec=120, got %d", uid, playTime)
		}
	}

	// Verify chart_stats rollup
	var plays int
	if err := s.db.QueryRow("SELECT plays FROM chart_stats WHERE chart_id=42").Scan(&plays); err != nil {
		t.Fatal(err)
	}
	if plays != 3 {
		t.Errorf("expected chart plays=3, got %d", plays)
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
	if err := s.RecordMatch("r1", 1, "C1", []int{1001, 1002}, r1, 60); err != nil {
		t.Fatal(err)
	}

	r2 := map[int]config.RecordData{
		1001: {Player: 1001, Score: 200, Accuracy: 0.98},
		1002: {Player: 1002, Score: 180, Accuracy: 0.92},
	}
	if err := s.RecordMatch("r2", 1, "C1", []int{1001, 1002}, r2, 90); err != nil {
		t.Fatal(err)
	}

	// player_stats: each should have games=2, wins cumulative, play_time accumulated
	var games, wins, bestScore, playTime int
	var sumAcc float64
	if err := s.db.QueryRow("SELECT games, wins, sum_acc, best_score, play_time_sec FROM player_stats WHERE user_id=1001").Scan(&games, &wins, &sumAcc, &bestScore, &playTime); err != nil {
		t.Fatal(err)
	}
	if games != 2 {
		t.Errorf("user 1001: games=%d, want 2", games)
	}
	if wins != 2 {
		t.Errorf("user 1001: wins=%d, want 2", wins)
	}
	if bestScore != 200 {
		t.Errorf("user 1001: best_score=%d, want 200", bestScore)
	}
	if playTime != 150 {
		t.Errorf("user 1001: play_time_sec=%d, want 150", playTime)
	}
	if sumAcc < 0.95+0.98-0.001 || sumAcc > 0.95+0.98+0.001 {
		t.Errorf("user 1001: sum_acc=%f, want ~%f", sumAcc, 0.95+0.98)
	}

	// chart_stats: plays=4
	var plays int
	if err := s.db.QueryRow("SELECT plays FROM chart_stats WHERE chart_id=1").Scan(&plays); err != nil {
		t.Fatal(err)
	}
	if plays != 4 {
		t.Errorf("chart plays=%d, want 4", plays)
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
	if err := s.RecordMatch("r1", 0, "", []int{1001}, r, 45); err != nil {
		t.Fatal(err)
	}

	p, err := s.GetPlayerProfile(1001)
	if err != nil {
		t.Fatalf("GetPlayerProfile: %v", err)
	}
	if p.Games != 1 {
		t.Errorf("games=%d, want 1", p.Games)
	}
	if p.PlayTimeSec != 45 {
		t.Errorf("play_time_sec=%d, want 45", p.PlayTimeSec)
	}
	if p.TotalScore != 900000 {
		t.Errorf("total_score=%d, want 900000", p.TotalScore)
	}
	if p.AvgAcc != 0.98 {
		t.Errorf("avg_acc=%f, want 0.98", p.AvgAcc)
	}
}

func TestLeaderboard(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// Player 1002 plays more
	s.RecordMatch("r1", 0, "", []int{1001}, map[int]config.RecordData{1001: {Player: 1001, Score: 100, Accuracy: 0.9}}, 30)
	s.RecordMatch("r2", 0, "", []int{1002}, map[int]config.RecordData{1002: {Player: 1002, Score: 200, Accuracy: 0.95}}, 60)
	s.RecordMatch("r3", 0, "", []int{1002}, map[int]config.RecordData{1002: {Player: 1002, Score: 150, Accuracy: 0.9}}, 40)

	// By playtime
	lb, err := s.GetLeaderboardByPlayTime(10)
	if err != nil {
		t.Fatalf("GetLeaderboardByPlayTime: %v", err)
	}
	if len(lb) < 2 {
		t.Fatalf("expected >= 2 entries, got %d", len(lb))
	}
	if lb[0].UserID != 1002 {
		t.Errorf("top playtime: want 1002, got %d", lb[0].UserID)
	}
	if lb[0].PlayTimeSec != 100 {
		t.Errorf("top playtime: want 100s, got %d", lb[0].PlayTimeSec)
	}

	// By total_score
	lb2, err := s.GetLeaderboardByTotalScore(10)
	if err != nil {
		t.Fatalf("GetLeaderboardByTotalScore: %v", err)
	}
	if lb2[0].UserID != 1002 {
		t.Errorf("top score: want 1002, got %d", lb2[0].UserID)
	}
}

func TestChartPopularity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	r := map[int]config.RecordData{
		1001: {Player: 1001, Score: 100, Accuracy: 0.9},
	}
	s.RecordMatch("r1", 42, "Hot Chart", []int{1001}, r, 30)
	s.RecordMatch("r2", 42, "Hot Chart", []int{1001}, map[int]config.RecordData{1001: {Player: 1001, Score: 200, Accuracy: 0.95}}, 45)

	cp, err := s.GetChartStats(42)
	if err != nil {
		t.Fatalf("GetChartStats: %v", err)
	}
	if cp.Plays != 2 {
		t.Errorf("plays=%d, want 2", cp.Plays)
	}
	if cp.Popularity <= 0 {
		t.Errorf("popularity should be > 0, got %f", cp.Popularity)
	}
	if cp.LastPlayedAt == "" {
		t.Error("last_played_at should not be empty")
	}

	// List
	list, err := s.GetChartPopularity(10)
	if err != nil {
		t.Fatalf("GetChartPopularity: %v", err)
	}
	if len(list) < 1 {
		t.Fatal("expected at least 1 chart")
	}
}

func TestCleanupDetail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	r := map[int]config.RecordData{
		1001: {Player: 1001, Score: 100, Accuracy: 0.95},
	}
	if err := s.RecordMatch("r1", 0, "", []int{1001}, r, 30); err != nil {
		t.Fatal(err)
	}

	// retentionDays=0 → keep forever
	if err := s.CleanupDetail(0); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM match_results").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("cleanup(0) should keep all, got %d", count)
	}

	// Age the match
	if _, err := s.db.Exec("UPDATE matches SET started_at = datetime('now', '-100 days')"); err != nil {
		t.Fatal(err)
	}
	if err := s.CleanupDetail(1); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM match_results").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0 match_results after cleanup on aged rows, got %d", count)
	}

	// player_stats rollup survives
	var games int
	if err := s.db.QueryRow("SELECT games FROM player_stats WHERE user_id=1001").Scan(&games); err != nil {
		t.Fatal(err)
	}
	if games != 1 {
		t.Errorf("player_stats should survive cleanup, games=%d", games)
	}
}

func TestOpenEmptyPath(t *testing.T) {
	_, err := Open("")
	if err == nil {
		t.Error("expected error for empty path")
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

func intPtr(i int) *int { return &i }
