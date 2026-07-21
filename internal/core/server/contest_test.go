package server

import (
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/config"
)

func TestContest_EnableAndWhitelistEnforcement(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	host := h.addUser(1, "host")
	room := NewRoom("room1", 1, 8, false)
	h.state.Rooms[room.ID] = room
	_ = host

	// 启用比赛模式，白名单显式给 {1, 5}。
	hub.EnableContest(room, []int{1, 5})
	if room.Contest == nil || !room.Contest.ManualStart || !room.Contest.AutoDisband {
		t.Fatal("contest should be enabled with manualStart + autoDisband")
	}
	// 白名单应含给定 id 与当前参与者（房主 1）。
	for _, id := range []int{1, 5} {
		if _, ok := room.Contest.Whitelist[id]; !ok {
			t.Errorf("whitelist should contain %d", id)
		}
	}

	// 非白名单用户被拒，白名单用户放行。
	outsider := NewUser(9, "out", "", h.state)
	if err := room.ValidateJoin(outsider, false); err != ErrNotWhitelisted {
		t.Errorf("non-whitelisted join should be ErrNotWhitelisted, got %v", err)
	}
	insider := NewUser(5, "in", "", h.state)
	if err := room.ValidateJoin(insider, false); err != nil {
		t.Errorf("whitelisted join should succeed, got %v", err)
	}
}

func TestContest_EnableDefaultsToParticipants(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	h.addUser(1, "host")
	room := NewRoom("room1", 1, 8, false)
	h.state.Rooms[room.ID] = room

	hub.EnableContest(room, nil) // 不给白名单 → 取当前参与者
	if _, ok := room.Contest.Whitelist[1]; !ok {
		t.Error("default whitelist should include current participant 1")
	}
}

func TestContest_SetWhitelistRequiresContest(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	h.addUser(1, "host")
	room := NewRoom("room1", 1, 8, false)
	h.state.Rooms[room.ID] = room

	if hub.SetContestWhitelist(room, []int{5}) {
		t.Error("setting whitelist on a non-contest room should return false")
	}
	hub.EnableContest(room, nil)
	if !hub.SetContestWhitelist(room, []int{5}) {
		t.Fatal("setting whitelist on a contest room should return true")
	}
	if _, ok := room.Contest.Whitelist[5]; !ok {
		t.Error("whitelist should contain 5")
	}
	if _, ok := room.Contest.Whitelist[1]; !ok {
		t.Error("whitelist should always include current participant 1")
	}
}

func TestContest_StartErrors(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	h.addUser(1, "host")
	room := NewRoom("room1", 1, 8, false)
	h.state.Rooms[room.ID] = room

	// 无比赛模式 → contest-room-not-found。
	if err := hub.StartContest(room, false); err == nil || err.Error() != "contest-room-not-found" {
		t.Errorf("start without contest should be contest-room-not-found, got %v", err)
	}
	hub.EnableContest(room, nil)
	// 非 WaitForReady（默认 SelectChart）→ room-not-waiting。
	if err := hub.StartContest(room, false); err == nil || err.Error() != "room-not-waiting" {
		t.Errorf("start while not waiting should be room-not-waiting, got %v", err)
	}
}

func TestContest_StartForce(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	h.addUser(1, "host")
	h.addUser(2, "p2")
	room := NewRoom("room1", 1, 8, false)
	room.AddUser(h.state.Users[2], false)
	room.Chart = &config.Chart{ID: 1, Name: "c"}
	room.State = StateWaitForReady{Started: map[int]struct{}{1: {}}} // 仅 1 就绪，2 未就绪
	h.state.Rooms[room.ID] = room
	hub.EnableContest(room, nil)

	// 非 force：未全员就绪 → not-all-ready。
	if err := hub.StartContest(room, false); err == nil || err.Error() != "not-all-ready" {
		t.Errorf("non-force start with unready player should be not-all-ready, got %v", err)
	}
	// force：强制开赛 → Playing。
	if err := hub.StartContest(room, true); err != nil {
		t.Fatalf("force start should succeed, got %v", err)
	}
	if _, playing := room.State.(StatePlaying); !playing {
		t.Errorf("room should be Playing after force start, got %T", room.State)
	}
}
