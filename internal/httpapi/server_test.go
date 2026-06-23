package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
	"github.com/Pimeng/gooophira-mp/internal/server"
)

func newTestService(t *testing.T, cfg *config.ServerConfig) (*Service, *server.ServerState) {
	t.Helper()
	if cfg == nil {
		cfg = &config.ServerConfig{}
	}
	state := server.NewServerState(cfg, nil, "test", "", "")
	svc := New(state, server.NewHub(state, nil))
	t.Cleanup(func() { _ = svc.Close() }) // 停止后台采样器，避免 goroutine 泄漏
	return svc, state
}

func addRoom(state *server.ServerState, id string, hostID int, hostName string, chart *config.Chart) {
	u := server.NewUser(hostID, hostName, "", state)
	state.Users[hostID] = u
	room := server.NewRoom(protocol.RoomID(id), hostID, 8, false)
	room.Chart = chart
	u.Room = room
	state.Rooms[protocol.RoomID(id)] = room
}

func do(svc *Service, method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	svc.route(w, httptest.NewRequest(method, path, nil))
	return w
}

func TestHTTP_RoomList(t *testing.T) {
	svc, state := newTestService(t, nil)
	addRoom(state, "room1", 1, "alice", &config.Chart{ID: 42, Name: "g.r.i.s"})
	addRoom(state, "_private", 2, "bob", nil) // 私有房间应被过滤

	w := do(svc, http.MethodGet, "/room")
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	var resp roomListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Rooms) != 1 {
		t.Fatalf("expected 1 public room (private filtered), got %d", len(resp.Rooms))
	}
	r := resp.Rooms[0]
	if r.RoomID != "room1" || r.Host.Name != "alice" || r.Host.ID != "1" {
		t.Errorf("room entry = %+v", r)
	}
	if r.State != "select_chart" {
		t.Errorf("state = %q, want select_chart", r.State)
	}
	if r.Chart == nil || r.Chart.ID != "42" || r.Chart.Name != "g.r.i.s" {
		t.Errorf("chart = %+v", r.Chart)
	}
	if len(r.Players) != 1 || r.Players[0].ID != 1 || r.Players[0].Name != "alice" {
		t.Errorf("players = %+v", r.Players)
	}
	if resp.Total != 1 {
		t.Errorf("total = %d, want 1", resp.Total)
	}
}

func TestHTTP_RoomStateStrings(t *testing.T) {
	svc, state := newTestService(t, nil)
	addRoom(state, "r1", 1, "a", nil)
	state.Rooms["r1"].State = server.StateWaitForReady{Started: map[int]struct{}{}}
	addRoom(state, "r2", 2, "b", nil)
	state.Rooms["r2"].State = server.StatePlaying{}

	w := do(svc, http.MethodGet, "/room")
	var resp roomListResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	got := map[string]string{}
	for _, r := range resp.Rooms {
		got[r.RoomID] = r.State
	}
	if got["r1"] != "waiting_for_ready" || got["r2"] != "playing" {
		t.Fatalf("state strings = %+v", got)
	}
}

func TestHTTP_ConfigEndpoints(t *testing.T) {
	svc, _ := newTestService(t, nil)
	for _, path := range []string{"/room-creation/config", "/replay/config"} {
		w := do(svc, http.MethodGet, path)
		if w.Code != 200 {
			t.Fatalf("%s status = %d", path, w.Code)
		}
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body["ok"] != true {
			t.Errorf("%s ok != true: %+v", path, body)
		}
	}
}

func TestHTTP_NotFound(t *testing.T) {
	svc, _ := newTestService(t, nil)
	if w := do(svc, http.MethodGet, "/nope"); w.Code != 404 {
		t.Errorf("unknown path status = %d, want 404", w.Code)
	}
}

func TestHTTP_CORSPreflight(t *testing.T) {
	origin := "https://t.phira.link"
	cfg := &config.ServerConfig{CorsOrigins: []string{origin}}
	svc, _ := newTestService(t, cfg)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/room", nil)
	req.Header.Set("Origin", origin)
	svc.route(w, req)
	if w.Code != http.StatusNoContent {
		t.Errorf("OPTIONS status = %d, want 204", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != origin {
		t.Errorf("CORS allow-origin = %q, want %q", got, origin)
	}
}

func TestHTTP_RateLimit(t *testing.T) {
	max := 2
	cfg := &config.ServerConfig{HTTPRateLimitMaxRequests: &max}
	svc, _ := newTestService(t, cfg)
	// 同一 IP 连发：前 2 次放行，第 3 次 429。
	_ = do(svc, http.MethodGet, "/replay/config")
	_ = do(svc, http.MethodGet, "/replay/config")
	if w := do(svc, http.MethodGet, "/replay/config"); w.Code != http.StatusTooManyRequests {
		t.Errorf("3rd request status = %d, want 429", w.Code)
	}
}
