package cli

import (
	"strings"
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/common/protocol"
	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/core/server"
)

func TestCLI_ContestEnableDisable(t *testing.T) {
	c, state, buf := newTestConsole(t)
	addRoom(state, "room1", 1)

	if out := c.run(buf, "contest room1 enable 1 2"); !strings.Contains(out, "room1") {
		t.Fatalf("enable output = %q", out)
	}
	state.Mu.Lock()
	room := state.Rooms[protocol.RoomID("room1")]
	hasContest := room.Contest != nil
	state.Mu.Unlock()
	if !hasContest {
		t.Fatal("contest should be enabled")
	}

	c.run(buf, "contest room1 disable")
	state.Mu.Lock()
	stillContest := state.Rooms[protocol.RoomID("room1")].Contest != nil
	state.Mu.Unlock()
	if stillContest {
		t.Error("contest should be disabled")
	}
}

func TestCLI_ContestWhitelistRequiresContest(t *testing.T) {
	c, state, buf := newTestConsole(t)
	addRoom(state, "room1", 1)

	// 未启用 → not-enabled。
	if out := c.run(buf, "contest room1 whitelist 5"); strings.Contains(out, "updated") {
		t.Errorf("whitelist on non-contest room should fail, got %q", out)
	}
	c.run(buf, "contest room1 enable")
	if out := c.run(buf, "contest room1 whitelist 5"); !strings.Contains(out, "room1") {
		t.Fatalf("whitelist update output = %q", out)
	}
	state.Mu.Lock()
	_, has5 := state.Rooms[protocol.RoomID("room1")].Contest.Whitelist[5]
	state.Mu.Unlock()
	if !has5 {
		t.Error("whitelist should contain 5")
	}
	// 无 user id。
	if out := c.run(buf, "contest room1 whitelist"); out == "" {
		t.Error("whitelist with no ids should print an error")
	}
}

func TestCLI_ContestStartForce(t *testing.T) {
	c, state, buf := newTestConsole(t)
	addRoom(state, "room1", 1)
	state.Mu.Lock()
	room := state.Rooms[protocol.RoomID("room1")]
	room.Chart = &config.Chart{ID: 1, Name: "c"}
	room.State = server.StateWaitForReady{Started: map[int]struct{}{}}
	state.Mu.Unlock()
	c.run(buf, "contest room1 enable")

	// 非 force：未就绪 → cannot start。
	if out := c.run(buf, "contest room1 start"); !strings.Contains(out, "Cannot start") {
		t.Errorf("non-force start should report cannot-start, got %q", out)
	}
	// force 模式：强制开赛。
	if out := c.run(buf, "contest room1 start force"); !strings.Contains(out, "room1") {
		t.Fatalf("force start output = %q", out)
	}
	state.Mu.Lock()
	_, playing := state.Rooms[protocol.RoomID("room1")].State.(server.StatePlaying)
	state.Mu.Unlock()
	if !playing {
		t.Error("room should be Playing after force start")
	}
}

func TestCLI_ContestUsage(t *testing.T) {
	c, _, buf := newTestConsole(t)
	if out := c.run(buf, "contest"); !strings.Contains(strings.ToLower(out), "usage") {
		t.Errorf("contest with no args should show usage, got %q", out)
	}
	if out := c.run(buf, "contest room1 bogus"); out == "" {
		t.Error("unknown subcommand should print an error")
	}
}
