package stats

import (
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

	mr, err := s.RecordMatch("room-abc", 42, "Test Chart", userIDs, results, names, 120.5)
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
	s.RecordMatch("r1", 1, "C1", []int{1001, 1002}, r1, nil, 60)

	r2 := map[int]config.RecordData{
		1001: {Player: 1001, Score: 200, Accuracy: 0.98},
		1002: {Player: 1002, Score: 180, Accuracy: 0.92},
	}
	s.RecordMatch("r2", 1, "C1", []int{1001, 1002}, r2, nil, 90)

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
	mr, err := s.RecordMatch("r1", 0, "", []int{1001, 1002}, r, nil, 30)
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
	s.RecordMatch("r1", 0, "", []int{1001}, r, map[int]string{1001: "Alice"}, 45)

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

	s.RecordMatch("r1", 0, "", []int{1001}, map[int]config.RecordData{1001: {Player: 1001, Score: 100, Accuracy: 0.9}}, nil, 30)
	s.RecordMatch("r2", 0, "", []int{1002}, map[int]config.RecordData{1002: {Player: 1002, Score: 200, Accuracy: 0.95}}, nil, 60)
	s.RecordMatch("r3", 0, "", []int{1002}, map[int]config.RecordData{1002: {Player: 1002, Score: 150, Accuracy: 0.9}}, nil, 40)

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

	s.RecordMatch("r1", 42, "Alpha", []int{1001}, map[int]config.RecordData{1001: {Player: 1001, Score: 100, Accuracy: 0.9}}, nil, 30)
	time.Sleep(1 * time.Second)
	s.RecordMatch("r2", 43, "Beta", []int{1001}, map[int]config.RecordData{1001: {Player: 1001, Score: 200, Accuracy: 0.95}}, nil, 45)
	time.Sleep(1 * time.Second)
	s.RecordMatch("r3", 42, "Alpha", []int{1002}, map[int]config.RecordData{1002: {Player: 1002, Score: 300, Accuracy: 0.99}}, nil, 60)

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

	s.RecordMatch("r1", 42, "Hot Chart", []int{1001}, map[int]config.RecordData{1001: {Player: 1001, Score: 100, Accuracy: 0.9}}, nil, 30)
	s.RecordMatch("r2", 42, "Hot Chart", []int{1001}, map[int]config.RecordData{1001: {Player: 1001, Score: 200, Accuracy: 0.95}}, nil, 45)

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

	s.RecordMatch("r1", 0, "", []int{1001}, map[int]config.RecordData{1001: {Player: 1001, Score: 100, Accuracy: 0.95}}, nil, 30)

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

func intPtr(i int) *int  { return &i }
func mathAbs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
