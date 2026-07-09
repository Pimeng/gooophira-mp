package cli

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/protocol"
	"github.com/Pimeng/gooophira-mp/internal/server"
)

// addRoomWithUsers 创建房间并把 host 及额外玩家注册到 room.usersMap（经 AddUser），
// 供 sethost/roominfo 等需要 ContainsUser 或 ParticipantsSnapshot 的命令测试。
func addRoomWithUsers(state *server.ServerState, id string, hostID int, extraIDs ...int) {
	addRoom(state, id, hostID)
	room := state.Rooms[protocol.RoomID(id)]
	room.Mu.Lock()
	room.AddUser(state.Users[hostID], false)
	for _, uid := range extraIDs {
		u := server.NewUser(uid, "u"+strconv.Itoa(uid), "", state)
		u.Room = room
		state.Users[uid] = u
		room.AddUser(u, false)
	}
	room.Mu.Unlock()
}

// ---- lock ----

func TestCLI_Lock_OnOffTogglesField(t *testing.T) {
	c, state, buf := newTestConsole(t)
	addRoom(state, "room1", 1)

	if out := c.run(buf, "lock room1 on"); !strings.Contains(out, "Locked room room1") {
		t.Errorf("lock on should confirm, got %q", out)
	}
	state.Mu.Lock()
	room := state.Rooms[protocol.RoomID("room1")]
	room.Mu.Lock()
	locked := room.Locked
	room.Mu.Unlock()
	state.Mu.Unlock()
	if !locked {
		t.Error("room.Locked should be true after 'lock on'")
	}

	if out := c.run(buf, "lock room1 off"); !strings.Contains(out, "Unlocked room room1") {
		t.Errorf("lock off should confirm, got %q", out)
	}
	state.Mu.Lock()
	room = state.Rooms[protocol.RoomID("room1")]
	room.Mu.Lock()
	locked = room.Locked
	room.Mu.Unlock()
	state.Mu.Unlock()
	if locked {
		t.Error("room.Locked should be false after 'lock off'")
	}
}

func TestCLI_Lock_BadToggle(t *testing.T) {
	c, state, buf := newTestConsole(t)
	addRoom(state, "room1", 1)
	if out := c.run(buf, "lock room1 maybe"); !strings.Contains(out, "Invalid") {
		t.Errorf("lock with bad toggle should error, got %q", out)
	}
}

func TestCLI_Lock_RoomNotFound(t *testing.T) {
	c, _, buf := newTestConsole(t)
	if out := c.run(buf, "lock missing on"); !strings.Contains(out, "not found") {
		t.Errorf("lock on missing room should error, got %q", out)
	}
}

func TestCLI_Lock_Usage(t *testing.T) {
	c, _, buf := newTestConsole(t)
	if out := c.run(buf, "lock"); !strings.Contains(strings.ToLower(out), "usage") {
		t.Errorf("lock with no args should show usage, got %q", out)
	}
}

// ---- cycle ----

func TestCLI_Cycle_OnOffTogglesField(t *testing.T) {
	c, state, buf := newTestConsole(t)
	addRoom(state, "room1", 1)

	if out := c.run(buf, "cycle room1 on"); !strings.Contains(out, "Enabled cycle mode") {
		t.Errorf("cycle on should confirm, got %q", out)
	}
	state.Mu.Lock()
	room := state.Rooms[protocol.RoomID("room1")]
	room.Mu.Lock()
	cycle := room.Cycle
	room.Mu.Unlock()
	state.Mu.Unlock()
	if !cycle {
		t.Error("room.Cycle should be true after 'cycle on'")
	}

	if out := c.run(buf, "cycle room1 off"); !strings.Contains(out, "Disabled cycle mode") {
		t.Errorf("cycle off should confirm, got %q", out)
	}
	state.Mu.Lock()
	room = state.Rooms[protocol.RoomID("room1")]
	room.Mu.Lock()
	cycle = room.Cycle
	room.Mu.Unlock()
	state.Mu.Unlock()
	if cycle {
		t.Error("room.Cycle should be false after 'cycle off'")
	}
}

func TestCLI_Cycle_BadToggle(t *testing.T) {
	c, state, buf := newTestConsole(t)
	addRoom(state, "room1", 1)
	if out := c.run(buf, "cycle room1 yes"); !strings.Contains(out, "Invalid") {
		t.Errorf("cycle with bad toggle should error, got %q", out)
	}
}

func TestCLI_Cycle_RoomNotFound(t *testing.T) {
	c, _, buf := newTestConsole(t)
	if out := c.run(buf, "cycle missing on"); !strings.Contains(out, "not found") {
		t.Errorf("cycle on missing room should error, got %q", out)
	}
}

func TestCLI_Cycle_Usage(t *testing.T) {
	c, _, buf := newTestConsole(t)
	if out := c.run(buf, "cycle"); !strings.Contains(strings.ToLower(out), "usage") {
		t.Errorf("cycle with no args should show usage, got %q", out)
	}
}

// ---- sethost ----

func TestCLI_SetHost_Success(t *testing.T) {
	c, state, buf := newTestConsole(t)
	addRoomWithUsers(state, "room1", 1, 2)

	out := c.run(buf, "sethost room1 2")
	if !strings.Contains(out, "Transferred") || !strings.Contains(out, "2") {
		t.Errorf("sethost success should confirm transfer to user 2, got %q", out)
	}
	state.Mu.Lock()
	room := state.Rooms[protocol.RoomID("room1")]
	room.Mu.Lock()
	hostID := room.HostID
	room.Mu.Unlock()
	state.Mu.Unlock()
	if hostID != 2 {
		t.Errorf("room.HostID = %d, want 2", hostID)
	}
}

func TestCLI_SetHost_UserNotInRoom(t *testing.T) {
	c, state, buf := newTestConsole(t)
	addRoomWithUsers(state, "room1", 1, 2)
	if out := c.run(buf, "sethost room1 99"); !strings.Contains(out, "not in room") {
		t.Errorf("sethost with absent user should error, got %q", out)
	}
}

func TestCLI_SetHost_AlreadyHost(t *testing.T) {
	c, state, buf := newTestConsole(t)
	addRoomWithUsers(state, "room1", 1, 2)
	if out := c.run(buf, "sethost room1 1"); !strings.Contains(out, "already host") {
		t.Errorf("sethost to current host should error, got %q", out)
	}
}

func TestCLI_SetHost_BadUserID(t *testing.T) {
	c, state, buf := newTestConsole(t)
	addRoomWithUsers(state, "room1", 1, 2)
	if out := c.run(buf, "sethost room1 abc"); !strings.Contains(out, "Invalid") {
		t.Errorf("sethost with non-numeric userId should error, got %q", out)
	}
}

func TestCLI_SetHost_RoomNotFound(t *testing.T) {
	c, _, buf := newTestConsole(t)
	if out := c.run(buf, "sethost missing 1"); !strings.Contains(out, "not found") {
		t.Errorf("sethost on missing room should error, got %q", out)
	}
}

func TestCLI_SetHost_Usage(t *testing.T) {
	c, _, buf := newTestConsole(t)
	if out := c.run(buf, "sethost"); !strings.Contains(strings.ToLower(out), "usage") {
		t.Errorf("sethost with no args should show usage, got %q", out)
	}
}

// ---- roominfo ----

func TestCLI_RoomInfo_FullOutput(t *testing.T) {
	c, state, buf := newTestConsole(t)
	addRoomWithUsers(state, "room1", 1, 2, 3)

	out := c.run(buf, "roominfo room1")
	for _, want := range []string{"Room room1 info", "Host: 1", "Players", "Monitors", "Chart"} {
		if !strings.Contains(out, want) {
			t.Errorf("roominfo output missing %q, got %q", want, out)
		}
	}
}

func TestCLI_RoomInfo_RoomNotFound(t *testing.T) {
	c, _, buf := newTestConsole(t)
	if out := c.run(buf, "roominfo missing"); !strings.Contains(out, "not found") {
		t.Errorf("roominfo on missing room should error, got %q", out)
	}
}

func TestCLI_RoomInfo_Usage(t *testing.T) {
	c, _, buf := newTestConsole(t)
	if out := c.run(buf, "roominfo"); !strings.Contains(strings.ToLower(out), "usage") {
		t.Errorf("roominfo with no args should show usage, got %q", out)
	}
}
