package cli

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/common/protocol"
	"github.com/Pimeng/gooophira-mp/internal/core/server"
)

// addCycleRoomWithUsers 创建一个 cycle 房间并加入额外玩家，便于 nexthost 测试。
func addCycleRoomWithUsers(state *server.ServerState, id string, hostID int, extraIDs ...int) {
	addRoom(state, id, hostID)
	room := state.Rooms[protocol.RoomID(id)]
	for _, uid := range extraIDs {
		u := server.NewUser(uid, "u"+strconv.Itoa(uid), "", state)
		u.Room = room
		state.Users[uid] = u
	}
	room.Mu.Lock()
	room.Cycle = true
	for _, uid := range extraIDs {
		room.AddUser(state.Users[uid], false)
	}
	room.Mu.Unlock()
}

func TestCLI_NextHost_RequiresCycleMode(t *testing.T) {
	c, state, buf := newTestConsole(t)
	addRoom(state, "room1", 1) // 非 cycle 房间

	if out := c.run(buf, "nexthost room1 1"); !strings.Contains(out, "cycle") {
		t.Errorf("nexthost on non-cycle room should error about cycle, got %q", out)
	}
}

func TestCLI_NextHost_UserNotInRoom(t *testing.T) {
	c, state, buf := newTestConsole(t)
	addCycleRoomWithUsers(state, "room1", 1, 2)

	if out := c.run(buf, "nexthost room1 99"); !strings.Contains(out, "not in room") {
		t.Errorf("nexthost with user not in room should error, got %q", out)
	}
}

func TestCLI_NextHost_RoomNotFound(t *testing.T) {
	c, _, buf := newTestConsole(t)
	if out := c.run(buf, "nexthost missing 1"); !strings.Contains(out, "not found") {
		t.Errorf("nexthost on missing room should error, got %q", out)
	}
}

func TestCLI_NextHost_BadUserID(t *testing.T) {
	c, state, buf := newTestConsole(t)
	addCycleRoomWithUsers(state, "room1", 1, 2)

	if out := c.run(buf, "nexthost room1 abc"); !strings.Contains(out, "Invalid") {
		t.Errorf("nexthost with non-numeric userId should error, got %q", out)
	}
}

func TestCLI_NextHost_Usage(t *testing.T) {
	c, _, buf := newTestConsole(t)
	if out := c.run(buf, "nexthost"); !strings.Contains(strings.ToLower(out), "usage") {
		t.Errorf("nexthost with no args should show usage, got %q", out)
	}
}

func TestCLI_NextHost_SetsNextHostID(t *testing.T) {
	c, state, buf := newTestConsole(t)
	addCycleRoomWithUsers(state, "room1", 1, 2, 3)

	if out := c.run(buf, "nexthost room1 3"); !strings.Contains(out, "3") {
		t.Errorf("nexthost should echo userId, got %q", out)
	}

	state.Mu.Lock()
	room := state.Rooms[protocol.RoomID("room1")]
	room.Mu.Lock()
	id, ok := room.NextHostID()
	room.Mu.Unlock()
	state.Mu.Unlock()

	if !ok || id != 3 {
		t.Errorf("nextHostID should be 3 after set, got (%d, %v)", id, ok)
	}
}

// TestCLI_NextHost_Overwrite 验证重复设置会覆盖前值。
func TestCLI_NextHost_Overwrite(t *testing.T) {
	c, state, buf := newTestConsole(t)
	addCycleRoomWithUsers(state, "room1", 1, 2, 3)

	c.run(buf, "nexthost room1 2")
	c.run(buf, "nexthost room1 3")

	state.Mu.Lock()
	room := state.Rooms[protocol.RoomID("room1")]
	room.Mu.Lock()
	id, ok := room.NextHostID()
	room.Mu.Unlock()
	state.Mu.Unlock()

	if !ok || id != 3 {
		t.Errorf("nextHostID should be 3 after overwrite, got (%d, %v)", id, ok)
	}
}

// TestCLI_NextHost_DoesNotAffectNonCycleRoom 验证非 cycle 房间不会设置 nextHostID。
func TestCLI_NextHost_DoesNotAffectNonCycleRoom(t *testing.T) {
	c, state, buf := newTestConsole(t)
	addRoom(state, "room1", 1) // 非 cycle

	c.run(buf, "nexthost room1 1") // 应失败

	state.Mu.Lock()
	room := state.Rooms[protocol.RoomID("room1")]
	room.Mu.Lock()
	_, ok := room.NextHostID()
	room.Mu.Unlock()
	state.Mu.Unlock()

	if ok {
		t.Error("nextHostID should not be set on non-cycle room")
	}
}
