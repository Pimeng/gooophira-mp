package cli

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/common/protocol"
	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/core/server"
)

// bufPrinter 把各类输出统一写入缓冲（忽略 kind），供基于文本内容的断言使用。
type bufPrinter struct{ buf *bytes.Buffer }

func (p *bufPrinter) emit(_ outputKind, s string) { fmt.Fprintln(p.buf, s) }

func newTestConsole(t *testing.T) (*Console, *server.ServerState, *bytes.Buffer) {
	t.Helper()
	en := "en-US"
	cfg := &config.ServerConfig{Lang: &en} // 用英文便于断言
	state := server.NewServerState(cfg, nil, "test", "", "")
	c := New(state, server.NewHub(state, nil), nil)
	buf := &bytes.Buffer{}
	c.out = &bufPrinter{buf}
	return c, state, buf
}

func (c *Console) run(buf *bytes.Buffer, line string) string {
	buf.Reset()
	c.Dispatch(line)
	return buf.String()
}

func addRoom(state *server.ServerState, id string, hostID int) {
	u := server.NewUser(hostID, "host", "", state)
	state.Users[hostID] = u
	room := server.NewRoom(protocol.RoomID(id), hostID, 8, false)
	u.Room = room
	state.Rooms[protocol.RoomID(id)] = room
}

// stubSession 实现 server.Session + adminDisconnecter，记录断开调用以供断言。
type stubSession struct {
	id            string
	closed        bool
	disconnectArg *bool // 记录 AdminDisconnect 的 preserveRoom 入参
}

func (s *stubSession) ID() string                        { return s.id }
func (s *stubSession) TrySend(protocol.ServerCommand)    {}
func (s *stubSession) TrySendFrame([]byte)               {}
func (s *stubSession) TrySendFrameOwned([]byte)          {}
func (s *stubSession) Close()                            { s.closed = true }
func (s *stubSession) AdminDisconnect(preserveRoom bool) { s.disconnectArg = &preserveRoom }

func TestCLI_KickDefault(t *testing.T) {
	c, state, buf := newTestConsole(t)
	u := server.NewUser(5, "bob", "", state)
	sess := &stubSession{id: "s5"}
	u.SetSession(sess)
	state.Users[5] = u

	if out := c.run(buf, "kick 5"); !strings.Contains(out, "5") {
		t.Errorf("kick output = %q", out)
	}
	if sess.disconnectArg == nil || *sess.disconnectArg != false {
		t.Errorf("default kick should AdminDisconnect(false), got %v", sess.disconnectArg)
	}
}

func TestCLI_KickPreserve(t *testing.T) {
	c, state, buf := newTestConsole(t)
	u := server.NewUser(5, "bob", "", state)
	sess := &stubSession{id: "s5"}
	u.SetSession(sess)
	state.Users[5] = u

	c.run(buf, "kick 5 preserve")
	if sess.disconnectArg == nil || *sess.disconnectArg != true {
		t.Errorf("kick preserve should AdminDisconnect(true), got %v", sess.disconnectArg)
	}
	// "true" 同义。
	sess.disconnectArg = nil
	c.run(buf, "kick 5 true")
	if sess.disconnectArg == nil || *sess.disconnectArg != true {
		t.Errorf("kick true should AdminDisconnect(true), got %v", sess.disconnectArg)
	}
}

func TestCLI_KickPreservePlayingAborts(t *testing.T) {
	c, state, buf := newTestConsole(t)
	addRoom(state, "r1", 1)                  // 房主 1
	u := server.NewUser(5, "bob", "", state) // 房内另一名玩家
	sess := &stubSession{id: "s5"}
	u.SetSession(sess)
	room := state.Rooms["r1"]
	u.Room = room
	state.Users[5] = u
	room.State = server.StatePlaying{
		Results:           map[int]config.RecordData{},
		Aborted:           map[int]struct{}{},
		ReconnectNotified: map[int]struct{}{},
	}

	c.run(buf, "kick 5 preserve")

	st, ok := room.State.(server.StatePlaying)
	if !ok {
		t.Fatalf("room should still be playing, got %T", room.State)
	}
	if _, aborted := st.Aborted[5]; !aborted {
		t.Error("kick preserve on a playing user should abort their run")
	}
	if sess.disconnectArg == nil || *sess.disconnectArg != true {
		t.Error("playing user kick preserve should still AdminDisconnect(true)")
	}
}

func TestCLI_ListRooms(t *testing.T) {
	c, state, buf := newTestConsole(t)
	if out := c.run(buf, "list"); !strings.Contains(out, "No rooms currently") {
		t.Errorf("empty list = %q", out)
	}
	addRoom(state, "room1", 1)
	out := c.run(buf, "rooms")
	if !strings.Contains(out, "[room1]") || !strings.Contains(out, "Players: 1/8") {
		t.Errorf("room list = %q", out)
	}
}

func TestCLI_Users(t *testing.T) {
	c, state, buf := newTestConsole(t)
	if out := c.run(buf, "users"); !strings.Contains(out, "No users online") {
		t.Errorf("empty users = %q", out)
	}
	addRoom(state, "room1", 1)
	if out := c.run(buf, "users"); !strings.Contains(out, "host") {
		t.Errorf("users = %q", out)
	}
}

func TestCLI_Broadcast(t *testing.T) {
	c, state, buf := newTestConsole(t)
	addRoom(state, "room1", 1)
	if out := c.run(buf, "broadcast hello world"); !strings.Contains(out, "Broadcast to 1 rooms") {
		t.Errorf("broadcast = %q", out)
	}
}

func TestCLI_BanUnban(t *testing.T) {
	c, state, buf := newTestConsole(t)
	if out := c.run(buf, "ban 100"); !strings.Contains(out, "Banned user: 100") {
		t.Errorf("ban = %q", out)
	}
	if _, banned := state.BannedUsers[100]; !banned {
		t.Error("user 100 should be banned")
	}
	if out := c.run(buf, "banlist"); !strings.Contains(out, "100") {
		t.Errorf("banlist = %q", out)
	}
	if out := c.run(buf, "unban 100"); !strings.Contains(out, "Unbanned user: 100") {
		t.Errorf("unban = %q", out)
	}
	if _, banned := state.BannedUsers[100]; banned {
		t.Error("user 100 should be unbanned")
	}
}

func TestCLI_Toggles(t *testing.T) {
	c, state, buf := newTestConsole(t)
	c.run(buf, "replay on")
	if !state.ReplayEnabled {
		t.Error("replay should be enabled")
	}
	if out := c.run(buf, "replay off"); !strings.Contains(out, "Replay recording disabled") {
		t.Errorf("replay off = %q", out)
	}
	if state.ReplayEnabled {
		t.Error("replay should be disabled")
	}
	c.run(buf, "roomcreation off")
	if state.RoomCreationEnabled {
		t.Error("room creation should be disabled")
	}
}

func TestCLI_Maintenance(t *testing.T) {
	c, state, buf := newTestConsole(t)
	c.run(buf, "maintenance on please wait")
	if !state.Maintenance || state.MaintenanceMessage == nil || *state.MaintenanceMessage != "please wait" {
		t.Errorf("maintenance on failed: %v %v", state.Maintenance, state.MaintenanceMessage)
	}
	c.run(buf, "maintenance off")
	if state.Maintenance {
		t.Error("maintenance should be off")
	}
}

func TestCLI_UnknownCommand(t *testing.T) {
	c, _, buf := newTestConsole(t)
	if out := c.run(buf, "frobnicate"); !strings.Contains(out, "Unknown command: frobnicate") {
		t.Errorf("unknown = %q", out)
	}
}

func TestCLI_Stop(t *testing.T) {
	en := "en-US"
	cfg := &config.ServerConfig{Lang: &en}
	state := server.NewServerState(cfg, nil, "test", "", "")
	called := false
	c := New(state, server.NewHub(state, nil), func() { called = true })
	c.out = &bufPrinter{&bytes.Buffer{}}
	c.Dispatch("stop")
	if !called {
		t.Error("stop should invoke shutdown callback")
	}
}

func TestCLI_Disband(t *testing.T) {
	c, state, buf := newTestConsole(t)
	addRoom(state, "room1", 1)
	if out := c.run(buf, "disband room1"); !strings.Contains(out, "Disbanded room room1") {
		t.Errorf("disband = %q", out)
	}
	state.Mu.Lock()
	_, exists := state.Rooms["room1"]
	state.Mu.Unlock()
	if exists {
		t.Error("room should be gone after disband")
	}
}
