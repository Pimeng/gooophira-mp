package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/config"
)

func sp(s string) *string { return &s }

func doAuth(svc *Service, method, path, token, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	svc.route(w, r)
	return w
}

func TestAdmin_NoTokenConfigured(t *testing.T) {
	svc, _ := newTestService(t, nil) // 未配置 admin_token
	if w := doAuth(svc, http.MethodGet, "/admin/rooms", "anything", ""); w.Code != http.StatusForbidden {
		t.Errorf("no admin_token should give 403, got %d", w.Code)
	}
}

func TestAdmin_WrongTokenAndBan(t *testing.T) {
	cfg := &config.ServerConfig{AdminToken: sp("secret")}
	svc, _ := newTestService(t, cfg)
	// 连续 5 次错误 token → IP 封禁。
	for range adminMaxFailedPerIP {
		if w := doAuth(svc, http.MethodGet, "/admin/rooms", "wrong", ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("wrong token should give 401, got %d", w.Code)
		}
	}
	// 封禁后即使用对的 token 也被拒（IP 已封）。
	if w := doAuth(svc, http.MethodGet, "/admin/rooms", "secret", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("banned IP should stay 401 even with correct token, got %d", w.Code)
	}
}

func TestAdmin_RoomsAndUsers(t *testing.T) {
	cfg := &config.ServerConfig{AdminToken: sp("secret")}
	svc, state := newTestService(t, cfg)
	addRoom(state, "room1", 1, "alice", &config.Chart{ID: 42, Name: "c"})

	w := doAuth(svc, http.MethodGet, "/admin/rooms", "secret", "")
	if w.Code != 200 {
		t.Fatalf("rooms status = %d", w.Code)
	}
	var roomsResp struct {
		OK    bool `json:"ok"`
		Rooms []struct {
			RoomID string `json:"roomid"`
			Host   struct {
				Name string `json:"name"`
			} `json:"host"`
		} `json:"rooms"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &roomsResp)
	if !roomsResp.OK || len(roomsResp.Rooms) != 1 || roomsResp.Rooms[0].RoomID != "room1" {
		t.Fatalf("admin rooms = %s", w.Body.String())
	}

	w = doAuth(svc, http.MethodGet, "/admin/users", "secret", "")
	var usersResp struct {
		OK    bool `json:"ok"`
		Users []struct {
			ID int `json:"id"`
		} `json:"users"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &usersResp)
	if !usersResp.OK || len(usersResp.Users) != 1 || usersResp.Users[0].ID != 1 {
		t.Fatalf("admin users = %s", w.Body.String())
	}
}

func TestAdmin_Broadcast(t *testing.T) {
	cfg := &config.ServerConfig{AdminToken: sp("secret")}
	svc, state := newTestService(t, cfg)
	addRoom(state, "room1", 1, "alice", nil)

	w := doAuth(svc, http.MethodPost, "/admin/broadcast", "secret", `{"message":"hello all"}`)
	if w.Code != 200 {
		t.Fatalf("broadcast status = %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		OK    bool `json:"ok"`
		Rooms int  `json:"rooms"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.OK || resp.Rooms != 1 {
		t.Fatalf("broadcast resp = %s", w.Body.String())
	}
	// 空消息 → 400
	if w := doAuth(svc, http.MethodPost, "/admin/broadcast", "secret", `{"message":""}`); w.Code != 400 {
		t.Errorf("empty broadcast should be 400, got %d", w.Code)
	}
}

func TestAdmin_Disband(t *testing.T) {
	cfg := &config.ServerConfig{AdminToken: sp("secret")}
	svc, state := newTestService(t, cfg)
	addRoom(state, "room1", 1, "alice", nil)

	if w := doAuth(svc, http.MethodPost, "/admin/disband", "secret", `{"roomid":"room1"}`); w.Code != 200 {
		t.Fatalf("disband status = %d body=%s", w.Code, w.Body.String())
	}
	state.Mu.Lock()
	_, exists := state.Rooms["room1"]
	state.Mu.Unlock()
	if exists {
		t.Error("room should be removed after disband")
	}
	// 不存在的房间 → 404
	if w := doAuth(svc, http.MethodPost, "/admin/disband", "secret", `{"roomid":"nope"}`); w.Code != 404 {
		t.Errorf("disband missing room should be 404, got %d", w.Code)
	}
}

func TestAdmin_UnknownRoute(t *testing.T) {
	cfg := &config.ServerConfig{AdminToken: sp("secret")}
	svc, _ := newTestService(t, cfg)
	if w := doAuth(svc, http.MethodGet, "/admin/nope", "secret", ""); w.Code != 404 {
		t.Errorf("unknown admin route should be 404, got %d", w.Code)
	}
}
