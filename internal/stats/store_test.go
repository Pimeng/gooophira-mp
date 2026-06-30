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

	// Verify tables exist
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

	if err := s.RecordMatch("room-abc", 42, "Test Chart", userIDs, results); err != nil {
		t.Fatalf("RecordMatch: %v", err)
	}

	// Verify match row
	var n int
	if err := s.db.QueryRow("SELECT n, chart_id FROM matches WHERE room_id='room-abc'").Scan(&n, new(int)); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("expected n=3, got %d", n)
	}

	// Verify match_results: 3 rows
	var mrCount int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM match_results").Scan(&mrCount); err != nil {
		t.Fatal(err)
	}
	if mrCount != 3 {
		t.Errorf("expected 3 match_results, got %d", mrCount)
	}

	// Verify rank: player 1001 should be rank 1
	var rank int
	if err := s.db.QueryRow("SELECT rank FROM match_results WHERE user_id=1001").Scan(&rank); err != nil {
		t.Fatal(err)
	}
	if rank != 1 {
		t.Errorf("expected rank=1 for user 1001, got %d", rank)
	}

	// Verify player_stats rollup: each has games=1
	for _, uid := range []int{1001, 1002, 1003} {
		var games int
		if err := s.db.QueryRow("SELECT games FROM player_stats WHERE user_id=?", uid).Scan(&games); err != nil {
			t.Errorf("player_stats for %d: %v", uid, err)
		}
		if games != 1 {
			t.Errorf("user %d: expected games=1, got %d", uid, games)
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

	// Game 1: 2 players
	r1 := map[int]config.RecordData{
		1001: {Player: 1001, Score: 100, Accuracy: 0.95},
		1002: {Player: 1002, Score: 90, Accuracy: 0.90},
	}
	if err := s.RecordMatch("r1", 1, "C1", []int{1001, 1002}, r1); err != nil {
		t.Fatal(err)
	}

	// Game 2: same 2 players
	r2 := map[int]config.RecordData{
		1001: {Player: 1001, Score: 200, Accuracy: 0.98},
		1002: {Player: 1002, Score: 180, Accuracy: 0.92},
	}
	if err := s.RecordMatch("r2", 1, "C1", []int{1001, 1002}, r2); err != nil {
		t.Fatal(err)
	}

	// player_stats: each should have games=2, wins cumulative, best_score updated
	var games, wins, bestScore int
	var sumAcc float64
	if err := s.db.QueryRow("SELECT games, wins, sum_acc, best_score FROM player_stats WHERE user_id=1001").Scan(&games, &wins, &sumAcc, &bestScore); err != nil {
		t.Fatal(err)
	}
	if games != 2 {
		t.Errorf("user 1001: games=%d, want 2", games)
	}
	if wins != 2 { // won both games (highest score)
		t.Errorf("user 1001: wins=%d, want 2", wins)
	}
	if bestScore != 200 {
		t.Errorf("user 1001: best_score=%d, want 200", bestScore)
	}
	if sumAcc < 0.95+0.98-0.001 || sumAcc > 0.95+0.98+0.001 {
		t.Errorf("user 1001: sum_acc=%f, want ~%f", sumAcc, 0.95+0.98)
	}

	// chart_stats: plays=4 (2 games × 2 players)
	var plays int
	if err := s.db.QueryRow("SELECT plays FROM chart_stats WHERE chart_id=1").Scan(&plays); err != nil {
		t.Fatal(err)
	}
	if plays != 4 {
		t.Errorf("chart plays=%d, want 4", plays)
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
	if err := s.RecordMatch("r1", 0, "", []int{1001}, r); err != nil {
		t.Fatal(err)
	}

	// retentionDays=0 is "keep forever" — should do nothing.
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

	// Manually age the match row to make it eligible for cleanup.
	if _, err := s.db.Exec("UPDATE matches SET started_at = datetime('now', '-100 days')"); err != nil {
		t.Fatal(err)
	}

	// retentionDays=1 should now delete the aged match_results.
	if err := s.CleanupDetail(1); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM match_results").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0 match_results after cleanup(1) on aged rows, got %d", count)
	}

	// player_stats rollup should survive cleanup.
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

func intPtr(i int) *int { return &i }
