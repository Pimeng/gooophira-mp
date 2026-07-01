package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/protocol"
	"github.com/Pimeng/gooophira-mp/internal/server"
)

type fakeSession struct {
	id     string
	closed bool
}

func (f *fakeSession) ID() string                     { return f.id }
func (f *fakeSession) TrySend(protocol.ServerCommand) {}
func (f *fakeSession) TrySendFrame([]byte)             {}
func (f *fakeSession) Close()                         { f.closed = true }

func addUser(state *server.ServerState, id int, name string, sess server.Session) *server.User {
	u := server.NewUser(id, name, "", state)
	if sess != nil {
		u.SetSession(sess)
	}
	state.Users[id] = u
	return u
}

func TestAdminUsers_GetSingle(t *testing.T) {
	svc, state := newTestService(t, adminCfg())
	addUser(state, 42, "alice", &fakeSession{id: "s1"})

	w := doAuth(svc, http.MethodGet, "/admin/users/42", "secret", "")
	if w.Code != 200 {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		OK   bool `json:"ok"`
		User struct {
			ID        int    `json:"id"`
			Name      string `json:"name"`
			Connected bool   `json:"connected"`
			Banned    bool   `json:"banned"`
		} `json:"user"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.OK || resp.User.ID != 42 || resp.User.Name != "alice" || !resp.User.Connected {
		t.Fatalf("unexpected user view: %s", w.Body.String())
	}

	// 不存在 → 404
	if w := doAuth(svc, http.MethodGet, "/admin/users/999", "secret", ""); w.Code != 404 {
		t.Errorf("missing user should be 404, got %d", w.Code)
	}
}

func TestAdminUsers_BanUserWithDisconnect(t *testing.T) {
	svc, state := newTestService(t, adminCfg())
	sess := &fakeSession{id: "s1"}
	addUser(state, 7, "bob", sess)

	w := doAuth(svc, http.MethodPost, "/admin/ban/user", "secret", `{"userId":7,"banned":true,"disconnect":true}`)
	if w.Code != 200 {
		t.Fatalf("ban status = %d body=%s", w.Code, w.Body.String())
	}
	state.Mu.Lock()
	_, banned := state.BannedUsers[7]
	state.Mu.Unlock()
	if !banned {
		t.Error("user 7 should be banned")
	}
	if !sess.closed {
		t.Error("session should be closed on ban+disconnect")
	}

	// 解封
	doAuth(svc, http.MethodPost, "/admin/ban/user", "secret", `{"userId":7,"banned":false}`)
	state.Mu.Lock()
	_, stillBanned := state.BannedUsers[7]
	state.Mu.Unlock()
	if stillBanned {
		t.Error("user 7 should be unbanned")
	}
}

func TestAdminUsers_BanUserBadID(t *testing.T) {
	svc, _ := newTestService(t, adminCfg())
	if w := doAuth(svc, http.MethodPost, "/admin/ban/user", "secret", `{"banned":true}`); w.Code != 400 {
		t.Errorf("missing userId should be 400, got %d", w.Code)
	}
	if w := doAuth(svc, http.MethodPost, "/admin/ban/user", "secret", `{"userId":1.5,"banned":true}`); w.Code != 400 {
		t.Errorf("non-integer userId should be 400, got %d", w.Code)
	}
}

func TestAdminUsers_BanRoom(t *testing.T) {
	svc, state := newTestService(t, adminCfg())
	w := doAuth(svc, http.MethodPost, "/admin/ban/room", "secret", `{"userId":5,"roomId":"room1","banned":true}`)
	if w.Code != 200 {
		t.Fatalf("ban-room status = %d body=%s", w.Code, w.Body.String())
	}
	state.Mu.Lock()
	_, banned := state.BannedRoomUsers[protocol.RoomID("room1")][5]
	state.Mu.Unlock()
	if !banned {
		t.Error("user 5 should be banned in room1")
	}

	// 解封后该房间集合应清空移除。
	doAuth(svc, http.MethodPost, "/admin/ban/room", "secret", `{"userId":5,"roomId":"room1","banned":false}`)
	state.Mu.Lock()
	_, exists := state.BannedRoomUsers[protocol.RoomID("room1")]
	state.Mu.Unlock()
	if exists {
		t.Error("empty room ban set should be removed")
	}

	// 缺 roomId → 400
	if w := doAuth(svc, http.MethodPost, "/admin/ban/room", "secret", `{"userId":5,"banned":true}`); w.Code != 400 {
		t.Errorf("missing roomId should be 400, got %d", w.Code)
	}
}

func TestAdminUsers_Move(t *testing.T) {
	svc, state := newTestService(t, adminCfg())
	// 离线用户 7 在 from 房间；目标房间 to。
	addRoom(state, "from", 1, "host", nil)
	addRoom(state, "to", 9, "host9", nil)
	u := addUser(state, 7, "mover", nil) // 无 session = 离线
	state.Mu.Lock()
	from := state.Rooms[protocol.RoomID("from")]
	from.AddUser(u, false)
	u.Room = from
	state.Mu.Unlock()

	w := doAuth(svc, http.MethodPost, "/admin/users/7/move", "secret", `{"roomId":"to","monitor":false}`)
	if w.Code != 200 {
		t.Fatalf("move status = %d body=%s", w.Code, w.Body.String())
	}
	state.Mu.Lock()
	movedTo := u.Room == state.Rooms[protocol.RoomID("to")]
	state.Mu.Unlock()
	if !movedTo {
		t.Error("user should be in the target room after move")
	}

	// 在线用户不可迁移 → 400。
	online := addUser(state, 8, "online", &fakeSession{id: "s"})
	state.Mu.Lock()
	online.Room = state.Rooms[protocol.RoomID("to")]
	state.Mu.Unlock()
	if w := doAuth(svc, http.MethodPost, "/admin/users/8/move", "secret", `{"roomId":"from"}`); w.Code != 400 {
		t.Errorf("moving online user should be 400, got %d", w.Code)
	}

	// 目标房间不存在 → 404。
	if w := doAuth(svc, http.MethodPost, "/admin/users/7/move", "secret", `{"roomId":"ghost"}`); w.Code != 404 {
		t.Errorf("move to missing room should be 404, got %d", w.Code)
	}
}

func TestAdminUsers_Disconnect(t *testing.T) {
	svc, state := newTestService(t, adminCfg())
	sess := &fakeSession{id: "s1"}
	addUser(state, 9, "carol", sess)

	if w := doAuth(svc, http.MethodPost, "/admin/users/9/disconnect", "secret", ""); w.Code != 200 {
		t.Fatalf("disconnect status = %d body=%s", w.Code, w.Body.String())
	}
	if !sess.closed {
		t.Error("session should be closed on disconnect")
	}

	// 离线用户（无 session）→ 404
	addUser(state, 10, "dave", nil)
	if w := doAuth(svc, http.MethodPost, "/admin/users/10/disconnect", "secret", ""); w.Code != 404 {
		t.Errorf("disconnecting offline user should be 404, got %d", w.Code)
	}
}
