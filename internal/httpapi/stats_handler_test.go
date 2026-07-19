package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/agentproto"
	"github.com/Pimeng/gooophira-mp/internal/agentstats"
	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/server"
	"github.com/Pimeng/gooophira-mp/internal/stats"
)

type localStatsProvider struct{ handler agentstats.QueryHandler }

func (p localStatsProvider) QueryStats(_ context.Context, method string, params any) (int, json.RawMessage, error) {
	data, err := json.Marshal(params)
	if err != nil {
		return 0, nil, err
	}
	response := p.handler.Handle(agentproto.QueryRequest{ID: "test", Method: method, Params: data})
	return response.StatusCode, response.Body, nil
}

func newStatsService(t *testing.T) (*Service, *stats.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test_stats.db")
	store, err := stats.Open(path)
	if err != nil {
		t.Fatalf("stats.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	// Seed: 3 players, 3 games with sequential timestamps
	r1 := map[int]config.RecordData{
		1001: {ID: 1, Player: 1001, Score: 900000, Accuracy: 0.98, Perfect: 800, Good: 50, MaxCombo: 850, FullCombo: true},
		1002: {ID: 2, Player: 1002, Score: 800000, Accuracy: 0.92, Perfect: 700, Good: 100, Bad: 30, Miss: 20, MaxCombo: 400},
		1003: {ID: 3, Player: 1003, Score: 700000, Accuracy: 0.88, Perfect: 600, Good: 150, Bad: 50, Miss: 50, MaxCombo: 300},
	}
	names := map[int]string{1001: "Alice", 1002: "Bob", 1003: "Carol"}
	if _, err := store.RecordMatch(context.Background(), "room-1", 42, "Test Chart", []int{1001, 1002, 1003}, r1, names, 120); err != nil {
		t.Fatalf("RecordMatch 1: %v", err)
	}
	time.Sleep(1 * time.Second)

	r2 := map[int]config.RecordData{
		1001: {ID: 4, Player: 1001, Score: 850000, Accuracy: 0.95, MaxCombo: 400},
		1002: {ID: 5, Player: 1002, Score: 950000, Accuracy: 0.99, MaxCombo: 900, FullCombo: true},
	}
	if _, err := store.RecordMatch(context.Background(), "room-2", 43, "Another Chart", []int{1001, 1002}, r2, names, 90); err != nil {
		t.Fatalf("RecordMatch 2: %v", err)
	}
	time.Sleep(1 * time.Second)

	r3 := map[int]config.RecordData{
		1003: {ID: 6, Player: 1003, Score: 750000, Accuracy: 0.90, MaxCombo: 350},
	}
	if _, err := store.RecordMatch(context.Background(), "room-3", 42, "Test Chart", []int{1003}, r3, names, 45); err != nil {
		t.Fatalf("RecordMatch 3: %v", err)
	}

	state := server.NewServerState(&config.ServerConfig{}, nil, "test", "", "")
	svc := New(state, server.NewHub(state, nil), localStatsProvider{handler: agentstats.QueryHandler{Store: store}})
	t.Cleanup(func() { _ = svc.Close() })
	return svc, store
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

func TestStats_PlayerProfile(t *testing.T) {
	svc, _ := newStatsService(t)

	w := do(svc, http.MethodGet, "/player/1001")
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	body := decode(t, w)
	if body["ok"] != true {
		t.Fatalf("ok != true: %v", body)
	}
	p := body["player"].(map[string]any)
	if p["name"] != "Alice" {
		t.Errorf("name = %v", p["name"])
	}
	if p["games"].(float64) != 2 {
		t.Errorf("games = %v", p["games"])
	}
	if p["wins"].(float64) != 1 {
		t.Errorf("wins = %v, want 1", p["wins"])
	}
	if p["play_time_sec"].(float64) != 210 {
		t.Errorf("play_time_sec = %v", p["play_time_sec"])
	}
	if p["rating"].(float64) == 1500 {
		t.Error("rating should have changed from baseline 1500")
	}
}

func TestStats_PlayerNotFound(t *testing.T) {
	svc, _ := newStatsService(t)
	w := do(svc, http.MethodGet, "/player/9999")
	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	body := decode(t, w)
	if body["ok"] != false {
		t.Error("expected ok=false for missing player")
	}
}

func TestStats_PlayerRecent(t *testing.T) {
	svc, _ := newStatsService(t)

	w := do(svc, http.MethodGet, "/player/1001/recent")
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	body := decode(t, w)
	if body["ok"] != true {
		t.Fatalf("ok != true: %v", body)
	}
	recent := body["recent"].([]any)
	if len(recent) != 2 {
		t.Fatalf("expected 2 recent matches, got %d", len(recent))
	}
	m1 := recent[0].(map[string]any)
	if m1["chart_name"] != "Another Chart" {
		t.Errorf("most recent chart = %v", m1["chart_name"])
	}
}

func TestStats_Leaderboard(t *testing.T) {
	svc, _ := newStatsService(t)

	w := do(svc, http.MethodGet, "/leaderboard?sort=playtime&limit=10")
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	body := decode(t, w)
	lb := body["leaderboard"].([]any)
	if len(lb) < 2 {
		t.Fatalf("expected >= 2 leaderboard entries, got %d", len(lb))
	}
	top := lb[0].(map[string]any)
	topName := top["name"].(string)
	if topName != "Alice" && topName != "Bob" {
		t.Errorf("top playtime = %s, want Alice or Bob", topName)
	}
}

func TestStats_LeaderboardByRating(t *testing.T) {
	svc, _ := newStatsService(t)

	w := do(svc, http.MethodGet, "/leaderboard")
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	body := decode(t, w)
	if body["sort"] != "rating" {
		t.Errorf("default sort = %v, want rating", body["sort"])
	}
	lb := body["leaderboard"].([]any)
	if len(lb) < 2 {
		t.Fatalf("expected >= 2 entries, got %d", len(lb))
	}
	for _, e := range lb {
		m := e.(map[string]any)
		if m["name"] == nil || m["name"] == "" {
			t.Errorf("missing name at rank %v", m["rank"])
		}
	}
}

func TestStats_ChartStats(t *testing.T) {
	svc, _ := newStatsService(t)

	w := do(svc, http.MethodGet, "/chart/42")
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	body := decode(t, w)
	c := body["chart"].(map[string]any)
	if c["name"] != "Test Chart" {
		t.Errorf("name = %v", c["name"])
	}
	if c["plays"].(float64) != 4 {
		t.Errorf("plays = %v, want 4", c["plays"])
	}
	if c["popularity"].(float64) <= 0 {
		t.Error("popularity should be > 0")
	}
}

func TestStats_ChartNotFound(t *testing.T) {
	svc, _ := newStatsService(t)
	w := do(svc, http.MethodGet, "/chart/999")
	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestStats_ChartsHot(t *testing.T) {
	svc, _ := newStatsService(t)

	w := do(svc, http.MethodGet, "/charts/hot")
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	body := decode(t, w)
	charts := body["charts"].([]any)
	if len(charts) < 2 {
		t.Fatalf("expected >= 2 chart entries, got %d", len(charts))
	}
	c1 := charts[0].(map[string]any)
	if c1["id"].(float64) != 42 {
		t.Errorf("hottest chart id = %v, want 42", c1["id"])
	}
}

func TestStats_StatsUnavailable(t *testing.T) {
	state := server.NewServerState(&config.ServerConfig{}, nil, "test", "", "")
	svc := New(state, server.NewHub(state, nil), nil)
	t.Cleanup(func() { _ = svc.Close() })

	for _, path := range []string{"/player/1", "/leaderboard", "/chart/1", "/charts/hot"} {
		w := do(svc, http.MethodGet, path)
		if w.Code != 503 {
			t.Errorf("%s status = %d, want 503", path, w.Code)
		}
	}
}

func TestStats_BadIDs(t *testing.T) {
	svc, _ := newStatsService(t)

	for _, path := range []string{"/player/abc", "/player/-1", "/chart/xyz"} {
		w := do(svc, http.MethodGet, path)
		if w.Code != 400 {
			t.Errorf("%s status = %d, want 400", path, w.Code)
		}
	}
}

func TestStats_LeaderboardLimitCap(t *testing.T) {
	svc, _ := newStatsService(t)

	w := do(svc, http.MethodGet, "/leaderboard?limit=200")
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	body := decode(t, w)
	lb := body["leaderboard"].([]any)
	if len(lb) != 3 {
		t.Errorf("expected 3 entries, got %d", len(lb))
	}
}

func TestStats_InvalidPlayerID(t *testing.T) {
	svc, _ := newStatsService(t)
	w := do(svc, http.MethodGet, "/player/")
	if w.Code != 400 {
		t.Errorf("empty ID status = %d, want 400", w.Code)
	}
}
