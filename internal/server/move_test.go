package server

import (
	"slices"
	"testing"
)

func offlineUserInRoom(h *testHarness, id int, room *Room) *User {
	u := NewUser(id, "u", "", h.state)
	h.state.Users[id] = u
	room.AddUser(u, false)
	u.Room = room
	return u
}

func TestMoveUser_Success(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	from := NewRoom("from", 1, 8, false) // 房主为 1。
	h.state.Rooms["from"] = from
	to := NewRoom("to", 9, 8, false) // 房主为 9。
	h.state.Rooms["to"] = to
	u := offlineUserInRoom(h, 2, from)

	if err := hub.MoveUser(u, to, false); err != nil {
		t.Fatalf("move should succeed, got %v", err)
	}
	if u.Room != to {
		t.Error("user.Room should be the target room")
	}
	if !slices.Contains(to.UserIDs(), 2) {
		t.Error("target room should contain the moved user")
	}
	if slices.Contains(from.UserIDs(), 2) {
		t.Error("source room should no longer contain the user")
	}
}

func TestMoveUser_DisbandsEmptySource(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	// 源房间只有 u（其为房主）→ 迁出后应解散。
	from := NewRoom("from", 2, 8, false)
	h.state.Rooms["from"] = from
	u := NewUser(2, "u", "", h.state)
	h.state.Users[2] = u
	u.Room = from // 已是房主，无需再 AddUser
	to := NewRoom("to", 9, 8, false)
	h.state.Rooms["to"] = to

	if err := hub.MoveUser(u, to, false); err != nil {
		t.Fatalf("move should succeed, got %v", err)
	}
	if _, exists := h.state.Rooms["from"]; exists {
		t.Error("empty source room should be disbanded")
	}
}

func TestMoveUser_Errors(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	from := NewRoom("from", 1, 8, false)
	h.state.Rooms["from"] = from
	to := NewRoom("to", 9, 8, false)
	h.state.Rooms["to"] = to

	// 在线用户（有会话）→ user-must-be-disconnected。
	online := offlineUserInRoom(h, 2, from)
	online.SetSession(&mockSession{id: "s"})
	if err := hub.MoveUser(online, to, false); err != errUserMustDisconnect {
		t.Errorf("online user move should be user-must-be-disconnected, got %v", err)
	}

	// 不在任何房间 → user-not-in-room。
	lobby := NewUser(3, "x", "", h.state)
	h.state.Users[3] = lobby
	if err := hub.MoveUser(lobby, to, false); err != ErrUserNotInRoom {
		t.Errorf("user not in room should be user-not-in-room, got %v", err)
	}

	// 源房间非 SelectChart → cannot-move-while-playing。
	u := offlineUserInRoom(h, 4, from)
	from.State = StatePlaying{}
	if err := hub.MoveUser(u, to, false); err != errCannotMovePlaying {
		t.Errorf("playing source should be cannot-move-while-playing, got %v", err)
	}
	from.State = StateSelectChart{}

	// 目标房间非 SelectChart → target-room-not-idle。
	to.State = StatePlaying{}
	if err := hub.MoveUser(u, to, false); err != errTargetRoomNotIdle {
		t.Errorf("non-idle target should be target-room-not-idle, got %v", err)
	}
}
