package httpapi

import (
	"net/http"
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/common/protocol"
	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/core/server"
)

func TestAdminContest_ConfigEnableDisable(t *testing.T) {
	svc, state := newTestService(t, adminCfg())
	addRoom(state, "room1", 1, "host", nil)

	// 启用 + 白名单 [1,2]。
	if w := doAuth(svc, http.MethodPost, "/admin/contest/rooms/room1/config", "secret", `{"enabled":true,"whitelist":[1,2]}`); w.Code != 200 {
		t.Fatalf("config enable status = %d body=%s", w.Code, w.Body.String())
	}
	state.Mu.Lock()
	room := state.Rooms[protocol.RoomID("room1")]
	hasContest := room.Contest != nil
	has2 := hasContest && func() bool { _, ok := room.Contest.Whitelist[2]; return ok }()
	state.Mu.Unlock()
	if !hasContest || !has2 {
		t.Fatalf("contest should be enabled with user 2 whitelisted (contest=%v)", hasContest)
	}

	// 停用。
	if w := doAuth(svc, http.MethodPost, "/admin/contest/rooms/room1/config", "secret", `{"enabled":false}`); w.Code != 200 {
		t.Fatalf("config disable status = %d", w.Code)
	}
	state.Mu.Lock()
	stillContest := state.Rooms[protocol.RoomID("room1")].Contest != nil
	state.Mu.Unlock()
	if stillContest {
		t.Error("contest should be disabled")
	}
}

func TestAdminContest_ConfigMissingRoom(t *testing.T) {
	svc, _ := newTestService(t, adminCfg())
	if w := doAuth(svc, http.MethodPost, "/admin/contest/rooms/nope/config", "secret", `{"enabled":true}`); w.Code != 404 {
		t.Errorf("config on missing room should be 404, got %d", w.Code)
	}
}

func TestAdminContest_Whitelist(t *testing.T) {
	svc, state := newTestService(t, adminCfg())
	addRoom(state, "room1", 1, "host", nil)

	// 未启用比赛 → whitelist 返回 404。
	if w := doAuth(svc, http.MethodPost, "/admin/contest/rooms/room1/whitelist", "secret", `{"userIds":[3]}`); w.Code != 404 {
		t.Errorf("whitelist on non-contest room should be 404, got %d", w.Code)
	}
	// 启用后再设白名单。
	doAuth(svc, http.MethodPost, "/admin/contest/rooms/room1/config", "secret", `{"enabled":true}`)
	if w := doAuth(svc, http.MethodPost, "/admin/contest/rooms/room1/whitelist", "secret", `{"userIds":[3,4]}`); w.Code != 200 {
		t.Fatalf("whitelist status = %d body=%s", w.Code, w.Body.String())
	}
	state.Mu.Lock()
	_, has3 := state.Rooms[protocol.RoomID("room1")].Contest.Whitelist[3]
	state.Mu.Unlock()
	if !has3 {
		t.Error("whitelist should contain user 3")
	}
	// 缺 userIds → 400。
	if w := doAuth(svc, http.MethodPost, "/admin/contest/rooms/room1/whitelist", "secret", `{}`); w.Code != 400 {
		t.Errorf("missing userIds should be 400, got %d", w.Code)
	}
}

func TestAdminContest_StartForce(t *testing.T) {
	svc, state := newTestService(t, adminCfg())
	addRoom(state, "room1", 1, "host", nil)
	state.Mu.Lock()
	room := state.Rooms[protocol.RoomID("room1")]
	room.Chart = &config.Chart{ID: 1, Name: "c"}
	room.State = server.StateWaitForReady{Started: map[int]struct{}{}} // 无人就绪
	state.Mu.Unlock()
	doAuth(svc, http.MethodPost, "/admin/contest/rooms/room1/config", "secret", `{"enabled":true}`)

	// 非 force：未全员就绪 → 400 not-all-ready。
	if w := doAuth(svc, http.MethodPost, "/admin/contest/rooms/room1/start", "secret", `{}`); w.Code != 400 {
		t.Errorf("non-force start should be 400, got %d body=%s", w.Code, w.Body.String())
	}
	// force → 200，房间进入 Playing。
	if w := doAuth(svc, http.MethodPost, "/admin/contest/rooms/room1/start", "secret", `{"force":true}`); w.Code != 200 {
		t.Fatalf("force start status = %d body=%s", w.Code, w.Body.String())
	}
	state.Mu.Lock()
	_, playing := state.Rooms[protocol.RoomID("room1")].State.(server.StatePlaying)
	state.Mu.Unlock()
	if !playing {
		t.Error("room should be Playing after force start")
	}
}

func TestAdminContest_StartMissingContest(t *testing.T) {
	svc, state := newTestService(t, adminCfg())
	addRoom(state, "room1", 1, "host", nil) // 未启用比赛
	if w := doAuth(svc, http.MethodPost, "/admin/contest/rooms/room1/start", "secret", `{}`); w.Code != 404 {
		t.Errorf("start without contest should be 404, got %d", w.Code)
	}
}
