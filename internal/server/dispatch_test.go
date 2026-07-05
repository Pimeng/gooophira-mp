package server

import (
	"bytes"
	"compress/flate"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
	"github.com/Pimeng/gooophira-mp/internal/replay"
)

// mockPhira 是测试用的 Phira API 桩。
type mockPhira struct {
	charts  map[int]config.Chart
	records map[int]config.RecordData
}

func (m *mockPhira) FetchUserInfo(ctx context.Context, token string) (PhiraUserInfo, error) {
	return PhiraUserInfo{}, nil
}
func (m *mockPhira) FetchChart(ctx context.Context, id int) (config.Chart, error) {
	if c, ok := m.charts[id]; ok {
		return c, nil
	}
	return config.Chart{}, errors.New("chart-fetch-failed")
}
func (m *mockPhira) FetchRecord(ctx context.Context, id int) (config.RecordData, error) {
	if r, ok := m.records[id]; ok {
		return r, nil
	}
	return config.RecordData{}, errors.New("record-fetch-failed")
}

func (h *testHarness) room(id protocol.RoomID) *Room { return h.state.Rooms[id] }

// mustDispatch 派发一条命令并断言其结果为 Ok，返回响应命令。
func (h *Hub) mustDispatch(t *testing.T, user *User, cmd protocol.ClientCommand) protocol.ServerCommand {
	t.Helper()
	resp, ok := h.ProcessClientCommand(user, cmd)
	if !ok {
		t.Fatalf("expected a response for %T", cmd)
	}
	switch c := resp.(type) {
	case protocol.SrvCreateRoom:
		assertOK(t, "CreateRoom", c.Result.Ok, c.Result.Error)
	case protocol.SrvJoinRoom:
		assertOK(t, "JoinRoom", c.Result.Ok, c.Result.Error)
	case protocol.SrvSelectChart:
		assertOK(t, "SelectChart", c.Result.Ok, c.Result.Error)
	case protocol.SrvRequestStart:
		assertOK(t, "RequestStart", c.Result.Ok, c.Result.Error)
	case protocol.SrvReady:
		assertOK(t, "Ready", c.Result.Ok, c.Result.Error)
	case protocol.SrvCancelReady:
		assertOK(t, "CancelReady", c.Result.Ok, c.Result.Error)
	case protocol.SrvPlayed:
		assertOK(t, "Played", c.Result.Ok, c.Result.Error)
	case protocol.SrvLeaveRoom:
		assertOK(t, "LeaveRoom", c.Result.Ok, c.Result.Error)
	default:
		t.Fatalf("unexpected response type %T", resp)
	}
	return resp
}

func assertOK(t *testing.T, name string, ok bool, errMsg string) {
	t.Helper()
	if !ok {
		t.Fatalf("%s failed: %s", name, errMsg)
	}
}

func TestDispatch_FullGameFlow(t *testing.T) {
	h := newHarness(200)
	phira := &mockPhira{
		charts: map[int]config.Chart{1: {ID: 1, Name: "chart1"}},
		records: map[int]config.RecordData{
			10: {ID: 10, Player: 1, Score: 900000, Accuracy: 0.95, Std: ptr64(0.02)},
			20: {ID: 20, Player: 2, Score: 980000, Accuracy: 0.99, Std: ptr64(0.01)},
		},
	}
	hub := NewHub(h.state, phira)
	alice := h.addUser(1, "alice")
	bob := h.addUser(2, "bob")

	// 建房
	hub.mustDispatch(t, alice, protocol.CmdCreateRoom{ID: "room1"})
	room := h.room("room1")
	if room == nil || room.HostID != 1 {
		t.Fatalf("room not created properly: %+v", room)
	}
	if alice.Room != room {
		t.Fatal("alice.Room should point to new room")
	}

	// bob 加入
	hub.mustDispatch(t, bob, protocol.CmdJoinRoom{ID: "room1", Monitor: false})
	if room.UserCount() != 2 {
		t.Fatalf("room should have 2 users, got %d", room.UserCount())
	}

	// 选谱
	hub.mustDispatch(t, alice, protocol.CmdSelectChart{ID: 1})
	if room.Chart == nil || room.Chart.ID != 1 {
		t.Fatalf("chart should be set to 1, got %+v", room.Chart)
	}

	// 请求开始 → WaitForReady（host 自动就绪）
	hub.mustDispatch(t, alice, protocol.CmdRequestStart{})
	st, ok := room.State.(StateWaitForReady)
	if !ok {
		t.Fatalf("state should be WaitForReady, got %T", room.State)
	}
	if _, ready := st.Started[1]; !ready {
		t.Error("host should be auto-ready after RequestStart")
	}

	// bob 就绪 → 全员就绪 → Playing
	hub.mustDispatch(t, bob, protocol.CmdReady{})
	if _, ok := room.State.(StatePlaying); !ok {
		t.Fatalf("state should be Playing after all ready, got %T", room.State)
	}

	// 双方交成绩 → 结算 → SelectChart
	hub.mustDispatch(t, alice, protocol.CmdPlayed{ID: 10})
	if _, ok := room.State.(StatePlaying); !ok {
		t.Fatal("should still be Playing after only alice played")
	}
	hub.mustDispatch(t, bob, protocol.CmdPlayed{ID: 20})
	if _, ok := room.State.(StateSelectChart); !ok {
		t.Fatalf("state should return to SelectChart after both played, got %T", room.State)
	}
}

func TestDispatch_AuthRepeated(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	alice := h.addUser(1, "alice")
	resp, ok := hub.ProcessClientCommand(alice, protocol.CmdAuthenticate{Token: "x"})
	if !ok {
		t.Fatal("auth should return a response")
	}
	auth, isAuth := resp.(protocol.SrvAuthenticate)
	if !isAuth || auth.Result.Ok {
		t.Fatalf("repeated authenticate should error, got %+v", resp)
	}
}

func TestDispatch_PingNoResponse(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	alice := h.addUser(1, "alice")
	if _, ok := hub.ProcessClientCommand(alice, protocol.CmdPing{}); ok {
		t.Error("Ping should not produce a response (handled at session layer)")
	}
}

func TestDispatch_CreateRoom_AlreadyInRoom(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	alice := h.addUser(1, "alice")
	hub.mustDispatch(t, alice, protocol.CmdCreateRoom{ID: "room1"})
	// 再次建房应失败（已在房间）
	resp, _ := hub.ProcessClientCommand(alice, protocol.CmdCreateRoom{ID: "room2"})
	if resp.(protocol.SrvCreateRoom).Result.Ok {
		t.Error("creating a second room while in one should fail")
	}
}

func TestDispatch_JoinRoom_NotFound(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	bob := h.addUser(2, "bob")
	resp, _ := hub.ProcessClientCommand(bob, protocol.CmdJoinRoom{ID: "nope", Monitor: false})
	if resp.(protocol.SrvJoinRoom).Result.Ok {
		t.Error("joining a non-existent room should fail")
	}
}

func TestDispatch_Played_WrongPlayer(t *testing.T) {
	h := newHarness()
	phira := &mockPhira{
		charts:  map[int]config.Chart{1: {ID: 1, Name: "c"}},
		records: map[int]config.RecordData{10: {ID: 10, Player: 999, Score: 1}}, // player 不是 alice
	}
	hub := NewHub(h.state, phira)
	alice := h.addUser(1, "alice")
	hub.mustDispatch(t, alice, protocol.CmdCreateRoom{ID: "room1"})
	hub.mustDispatch(t, alice, protocol.CmdSelectChart{ID: 1})
	hub.mustDispatch(t, alice, protocol.CmdRequestStart{})
	// 单人房，RequestStart 后立即进入 Playing
	if _, ok := h.room("room1").State.(StatePlaying); !ok {
		t.Fatalf("single-player room should enter Playing, got %T", h.room("room1").State)
	}
	resp, _ := hub.ProcessClientCommand(alice, protocol.CmdPlayed{ID: 10})
	if resp.(protocol.SrvPlayed).Result.Ok {
		t.Error("played with mismatched player id should fail (record-invalid)")
	}
}

func TestDispatch_ChatBroadcast(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	alice := h.addUser(1, "alice")
	hub.mustDispatch(t, alice, protocol.CmdCreateRoom{ID: "room1"})
	before := len(sentTo(alice))
	resp, _ := hub.ProcessClientCommand(alice, protocol.CmdChat{Message: "hello"})
	if c := resp.(protocol.SrvChat); !c.Result.Ok {
		t.Fatalf("chat should succeed: %s", c.Result.Error)
	}
	// alice 应收到自己的聊天广播（SrvMessage{MsgChat}）
	found := false
	for _, cmd := range sentTo(alice)[before:] {
		if sm, ok := cmd.(protocol.SrvMessage); ok {
			if chat, ok := sm.Message.(protocol.MsgChat); ok && chat.Content == "hello" && chat.User == 1 {
				found = true
			}
		}
	}
	if !found {
		t.Error("chat should be broadcast to room participants")
	}
}

func TestDispatch_LockAndCycle(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	alice := h.addUser(1, "alice")
	hub.mustDispatch(t, alice, protocol.CmdCreateRoom{ID: "room1"})
	room := h.room("room1")

	if resp, _ := hub.ProcessClientCommand(alice, protocol.CmdLockRoom{Lock: true}); !resp.(protocol.SrvLockRoom).Result.Ok {
		t.Error("host lock should succeed")
	}
	if !room.Locked {
		t.Error("room should be locked")
	}
	if resp, _ := hub.ProcessClientCommand(alice, protocol.CmdCycleRoom{Cycle: true}); !resp.(protocol.SrvCycleRoom).Result.Ok {
		t.Error("host cycle should succeed")
	}
	if !room.Cycle {
		t.Error("room should be in cycle mode")
	}
}

// TestDispatch_AbortThenSettle 对应 TS「游戏中玩家 Abort 后，房主交成绩即结算，
// 不再等待已中止的玩家」。
func TestDispatch_AbortThenSettle(t *testing.T) {
	h := newHarness()
	phira := &mockPhira{
		charts:  map[int]config.Chart{1: {ID: 1, Name: "c"}},
		records: map[int]config.RecordData{10: {ID: 10, Player: 1, Score: 1}},
	}
	hub := NewHub(h.state, phira)
	host := h.addUser(1, "host")
	player := h.addUser(2, "player")

	hub.mustDispatch(t, host, protocol.CmdCreateRoom{ID: "room1"})
	hub.mustDispatch(t, player, protocol.CmdJoinRoom{ID: "room1", Monitor: false})
	hub.mustDispatch(t, host, protocol.CmdSelectChart{ID: 1})
	hub.mustDispatch(t, host, protocol.CmdRequestStart{})
	hub.mustDispatch(t, player, protocol.CmdReady{})
	room := h.room("room1")
	if _, ok := room.State.(StatePlaying); !ok {
		t.Fatalf("should be Playing, got %T", room.State)
	}

	// player 中止
	if resp, _ := hub.ProcessClientCommand(player, protocol.CmdAbort{}); !resp.(protocol.SrvAbort).Result.Ok {
		t.Fatal("abort should succeed")
	}
	if _, ok := room.State.(StatePlaying); !ok {
		t.Fatal("should still be Playing after only player aborted (host unfinished)")
	}
	// host 交成绩 → 全员完成（host 成绩 + player 中止）→ 结算回 SelectChart
	hub.mustDispatch(t, host, protocol.CmdPlayed{ID: 10})
	if _, ok := room.State.(StateSelectChart); !ok {
		t.Fatalf("should settle to SelectChart (not waiting for aborted player), got %T", room.State)
	}
}

// TestDispatch_MonitorMustReady 对应 TS「观战者也需 Ready，否则状态机不推进」。
func TestDispatch_MonitorMustReady(t *testing.T) {
	h := newHarness(300) // 300 在 monitors 白名单
	phira := &mockPhira{charts: map[int]config.Chart{1: {ID: 1, Name: "c"}}}
	hub := NewHub(h.state, phira)
	host := h.addUser(1, "host")
	player := h.addUser(2, "player")
	monitor := h.addUser(300, "mon")

	hub.mustDispatch(t, host, protocol.CmdCreateRoom{ID: "room1"})
	hub.mustDispatch(t, player, protocol.CmdJoinRoom{ID: "room1", Monitor: false})
	hub.mustDispatch(t, monitor, protocol.CmdJoinRoom{ID: "room1", Monitor: true})
	hub.mustDispatch(t, host, protocol.CmdSelectChart{ID: 1})
	hub.mustDispatch(t, host, protocol.CmdRequestStart{})

	room := h.room("room1")
	hub.mustDispatch(t, player, protocol.CmdReady{})
	if _, ok := room.State.(StateWaitForReady); !ok {
		t.Fatal("should still wait: monitor has not readied yet")
	}
	hub.mustDispatch(t, monitor, protocol.CmdReady{})
	if _, ok := room.State.(StatePlaying); !ok {
		t.Fatalf("should enter Playing once monitor also readied, got %T", room.State)
	}
}

// TestDispatch_NonHostCannotSelectOrStart 对应 TS「非房主越权操作被拒」。
func TestDispatch_NonHostCannotSelectOrStart(t *testing.T) {
	h := newHarness()
	phira := &mockPhira{charts: map[int]config.Chart{111: {ID: 111, Name: "c"}}}
	hub := NewHub(h.state, phira)
	host := h.addUser(1, "host")
	player := h.addUser(2, "player")
	hub.mustDispatch(t, host, protocol.CmdCreateRoom{ID: "room1"})
	hub.mustDispatch(t, player, protocol.CmdJoinRoom{ID: "room1", Monitor: false})

	if resp, _ := hub.ProcessClientCommand(player, protocol.CmdSelectChart{ID: 999}); resp.(protocol.SrvSelectChart).Result.Ok {
		t.Error("non-host SelectChart should be rejected")
	}
	if resp, _ := hub.ProcessClientCommand(player, protocol.CmdRequestStart{}); resp.(protocol.SrvRequestStart).Result.Ok {
		t.Error("non-host RequestStart should be rejected")
	}
	if h.room("room1").Chart != nil {
		t.Error("chart should remain unset after rejected non-host select")
	}
}

// TestDispatch_RequestStartBroadcastsHintChat 验证房主下发「游戏开始」后，系统身份
// 延迟广播一条聊天提示，告知房间成员一分钟内准备。提示文本应包含房主名，所有房间成员
// （含房主自己）均应收到，且发送者 User 为 SystemChatUserID。
func TestDispatch_RequestStartBroadcastsHintChat(t *testing.T) {
	h := newHarness()
	phira := &mockPhira{charts: map[int]config.Chart{1: {ID: 1, Name: "c"}}}
	hub := NewHub(h.state, phira)
	host := h.addUser(1, "host")
	player := h.addUser(2, "player")

	SetProtocolHackDelay(0)
	t.Cleanup(func() { SetProtocolHackDelay(10 * time.Millisecond) })

	hub.mustDispatch(t, host, protocol.CmdCreateRoom{ID: "room1"})
	hub.mustDispatch(t, player, protocol.CmdJoinRoom{ID: "room1", Monitor: false})
	hub.mustDispatch(t, host, protocol.CmdSelectChart{ID: 1})

	hostBefore := len(sentTo(host))
	playerBefore := len(sentTo(player))
	hub.mustDispatch(t, host, protocol.CmdRequestStart{})
	time.Sleep(50 * time.Millisecond) // 等延迟派发完成

	expected := l10n.TL(h.state.ServerLang, "chat-game-start-hint", map[string]string{"user": "host"})
	sysID := h.state.SystemChatUserID()

	findHint := func(sent []protocol.ServerCommand) bool {
		for _, cmd := range sent {
			sm, ok := cmd.(protocol.SrvMessage)
			if !ok {
				continue
			}
			chat, ok := sm.Message.(protocol.MsgChat)
			if !ok {
				continue
			}
			if chat.Content == expected && chat.User == sysID {
				return true
			}
		}
		return false
	}

	if !findHint(sentTo(host)[hostBefore:]) {
		t.Error("host should receive game-start hint chat with system user id")
	}
	if !findHint(sentTo(player)[playerBefore:]) {
		t.Error("player should receive game-start hint chat with system user id")
	}
}

// TestDispatch_FramesDroppedWhenNotPlaying 对应 TS「非游玩状态丢弃触控/判定帧，不分发」。
func TestDispatch_FramesDroppedWhenNotPlaying(t *testing.T) {
	h := newHarness(300)
	hub := NewHub(h.state, &mockPhira{})
	host := h.addUser(1, "host")
	mon := h.addUser(300, "mon")
	hub.mustDispatch(t, host, protocol.CmdCreateRoom{ID: "room1"})
	hub.mustDispatch(t, mon, protocol.CmdJoinRoom{ID: "room1", Monitor: true})

	before := len(sentTo(mon))
	// SelectChart 态下注入触控/判定帧：应被丢弃，不广播给观战者，且无响应。
	frames := []protocol.TouchFrame{{Time: 10, Points: []protocol.TouchPoint{{ID: 1, Pos: protocol.CompactPos{X: 0, Y: 1}}}}}
	if _, ok := hub.ProcessClientCommand(host, protocol.CmdTouches{Frames: frames}); ok {
		t.Error("Touches in non-Playing state should produce no response")
	}
	if _, ok := hub.ProcessClientCommand(host, protocol.CmdJudges{Judges: []protocol.JudgeEvent{{Time: 10}}}); ok {
		t.Error("Judges in non-Playing state should produce no response")
	}
	if len(sentTo(mon)) != before {
		t.Error("monitor should not receive any frame while room is not Playing")
	}
}

func TestDispatch_LeaveRoomDisbands(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	alice := h.addUser(1, "alice")
	hub.mustDispatch(t, alice, protocol.CmdCreateRoom{ID: "room1"})
	hub.mustDispatch(t, alice, protocol.CmdLeaveRoom{})
	if h.room("room1") != nil {
		t.Error("room should be disbanded when last user leaves")
	}
}

// TestDispatch_ReplayWithFakeMonitor 对应 TS test/game/replay.test.ts
// "启用回放录制时，无观战者也能产生触控/判定录制数据"。
// 验证：sendFakeMonitorJoin 发送假观战者 → 客户端上报 Touches/Judges →
// 录制器正确落盘，文件包含游戏数据。
func TestDispatch_ReplayWithFakeMonitor(t *testing.T) {
	dir := t.TempDir()
	enabled := true
	cfg := &config.ServerConfig{ReplayEnabled: &enabled, ReplayBaseDir: &dir}
	st := NewServerState(cfg, nil, "test", "", "")
	rec := replay.NewRecorder(dir, nil)
	st.ReplayRecorder = rec

	phira := &mockPhira{
		charts:  map[int]config.Chart{1: {ID: 1, Name: "Chart-1"}},
		records: map[int]config.RecordData{10: {ID: 10, Player: 1, Score: 900000, Accuracy: 0.95, Std: ptr64(0.02)}},
	}
	hub := NewHub(st, phira)
	hub.OnEnterPlaying = func(room *Room) {
		if !st.ReplayEnabled || !room.ReplayEligible || room.Chart == nil {
			return
		}
		users := make([]replay.Participant, 0, room.UserCount())
		for _, id := range room.UserIDs() {
			name := ""
			if u := st.Users[id]; u != nil {
				name = u.Name
			}
			users = append(users, replay.Participant{ID: id, Name: name})
		}
		rec.StartRoom(room.ID, room.Chart.ID, room.Chart.Name, users)
	}
	hub.OnGameEnd = func(room *Room) { rec.EndRoom(room.ID) }

	// 添加用户到 testHarness 风格的状态
	alice := NewUser(1, "Alice", "", st)
	alice.SetSession(&mockSession{id: "alice"})
	st.Users[1] = alice

	// 1. 建房 → sendFakeMonitorJoin 应发送假观战者加入消息
	hub.mustDispatch(t, alice, protocol.CmdCreateRoom{ID: "room_replay"})
	room := st.Rooms["room_replay"]
	if room == nil {
		t.Fatal("room not created")
	}

	// 验证假观战者消息已发送给 alice（sendFakeMonitorJoin 延迟 20ms 后发送，模仿 TS setImmediate）
	time.Sleep(50 * time.Millisecond)
	var gotFakeOnJoin, gotFakeJoinMsg bool
	for _, cmd := range sentTo(alice) {
		switch c := cmd.(type) {
		case protocol.SrvOnJoinRoom:
			if c.Info.ID == replay.FakeMonitorID() && c.Info.Monitor {
				gotFakeOnJoin = true
			}
		case protocol.SrvMessage:
			if m, ok := c.Message.(protocol.MsgJoinRoom); ok && m.User == replay.FakeMonitorID() {
				gotFakeJoinMsg = true
			}
		}
	}
	if !gotFakeOnJoin {
		t.Error("fake monitor OnJoinRoom not sent to room creator")
	}
	if !gotFakeJoinMsg {
		t.Error("fake monitor JoinRoom message not sent to room creator")
	}

	// 建房后应同时收到一条系统聊天，明确告知玩家这是服务器模拟的回放采集会话、无需理会。
	hintContent, gotHint := findHintChat(sentTo(alice))
	if !gotHint {
		t.Error("expected a system chat hint after fake monitor join (sendFakeMonitorJoin path)")
	} else {
		// 提示文本应包含本地化的录制器显示名（带「（系统）」后缀），便于玩家对应。
		systemName := l10n.TL(st.ServerLang, "system-user-name", nil)
		if !strings.Contains(hintContent, systemName) {
			t.Errorf("hint chat should contain recorder name %q, got %q", systemName, hintContent)
		}
	}

	// 2. 选谱 → 请求开始（单人立即 Playing，触发 OnEnterPlaying → StartRoom）
	hub.mustDispatch(t, alice, protocol.CmdSelectChart{ID: 1})
	hub.mustDispatch(t, alice, protocol.CmdRequestStart{})
	if _, ok := room.State.(StatePlaying); !ok {
		t.Fatalf("single-player room should enter Playing, got %T", room.State)
	}

	// 3. 发送触摸帧和判定事件（客户端因为有假观战者所以上报）
	frames := []protocol.TouchFrame{
		{Time: 1, Points: []protocol.TouchPoint{{ID: 0, Pos: protocol.CompactPos{X: 0.5, Y: 0.5}}}},
	}
	judges := []protocol.JudgeEvent{
		{Time: 1, LineID: 1, NoteID: 2, Judgement: protocol.JudgePerfect},
	}
	if _, ok := hub.ProcessClientCommand(alice, protocol.CmdTouches{Frames: frames}); ok {
		t.Error("Touches should produce no response")
	}
	if _, ok := hub.ProcessClientCommand(alice, protocol.CmdJudges{Judges: judges}); ok {
		t.Error("Judges should produce no response")
	}

	// 4. 交成绩 → 结算（触发 OnGameEnd → EndRoom 写盘）
	hub.mustDispatch(t, alice, protocol.CmdPlayed{ID: 10})
	if _, ok := room.State.(StateSelectChart); !ok {
		t.Fatalf("should settle to SelectChart, got %T", room.State)
	}

	// 5. 读取回放文件，验证内容
	files := rec.ListRoomFiles("room_replay")
	if len(files) != 1 {
		t.Fatalf("expected 1 replay file, got %d", len(files))
	}
	raw, err := os.ReadFile(files[0].Path)
	if err != nil {
		t.Fatalf("read replay file: %v", err)
	}
	if len(raw) < 13 || string(raw[0:8]) != "PHIRAREC" {
		t.Fatalf("invalid replay file (len=%d, magic=%q)", len(raw), string(raw[:8]))
	}

	// 用 reader 验证解码后的数据
	hdr, err := replay.ReadReplayHeader(files[0].Path)
	if err != nil {
		t.Fatalf("ReadReplayHeader: %v", err)
	}
	if hdr.RecordID != 10 {
		t.Errorf("recordID = %d, want 10", hdr.RecordID)
	}
	if hdr.ChartID != 1 {
		t.Errorf("chartID = %d, want 1", hdr.ChartID)
	}
	if hdr.ChartName != "Chart-1" {
		t.Errorf("chartName = %q, want %q", hdr.ChartName, "Chart-1")
	}
	if hdr.UserID != 1 {
		t.Errorf("userID = %d, want 1", hdr.UserID)
	}
	if hdr.UserName != "Alice" {
		t.Errorf("userName = %q, want %q", hdr.UserName, "Alice")
	}

	// 验证录制文件包含实际游戏数据：解压并检查 touches/judges 数组非空。
	// 仅含元数据的 bug 录制文件 → 0 touches + 0 judges; 修复后应有数据。
	content, err := decodePhiraRecPayload(raw)
	if err != nil {
		t.Fatalf("decode replay payload: %v", err)
	}
	// buildContent 顺序: recordID(I32) + ts(I64) + chartID(I32) + chartName(str) + userID(I32) + userName(str) + touches(arr) + judges(arr)
	br := protocol.NewBinaryReader(content)
	_ = br.ReadI32()    // recordID
	_ = br.ReadI64()    // timestamp
	_ = br.ReadI32()    // chartID
	_ = br.ReadString() // chartName
	_ = br.ReadI32()    // userID
	_ = br.ReadString() // userName
	touchCount := readUleb(br)
	// 跳过触摸帧数据，以便读取 judges 计数
	for i := uint64(0); i < touchCount; i++ {
		_ = br.ReadF32()        // time
		ptCount := readUleb(br) // points count
		for j := uint64(0); j < ptCount; j++ {
			_ = br.ReadI8()  // point id
			_ = br.ReadU16() // x (F16 bits)
			_ = br.ReadU16() // y (F16 bits)
		}
	}
	judgeCount := readUleb(br)
	if touchCount == 0 {
		t.Errorf("touchCount = 0: touches not recorded — fake monitor may not be triggering client data")
	}
	if judgeCount == 0 {
		t.Errorf("judgeCount = 0: judges not recorded — fake monitor may not be triggering client data")
	}
	t.Logf("touchCount=%d, judgeCount=%d", touchCount, judgeCount)
}

// decodePhiraRecPayload 解压 PHIRAREC 文件载荷（检查 magic + version + compression 后解压）。
func decodePhiraRecPayload(raw []byte) ([]byte, error) {
	if len(raw) < 13 || string(raw[0:8]) != "PHIRAREC" {
		return nil, errors.New("not a PHIRAREC file")
	}
	compression := raw[12]
	payload := raw[13:]
	switch compression {
	case 0x00:
		return payload, nil
	case 0x02: // DEFLATE
		r := flate.NewReader(bytes.NewReader(payload))
		defer r.Close()
		return io.ReadAll(r)
	default:
		return nil, errors.New("unsupported compression")
	}
}

// ptr64 把 float64 字面量转为指针，便于填充 RecordData.Std / StdScore 之类的 *float64 字段。
func ptr64(v float64) *float64 { return &v }

// readUleb 从 BinaryReader 读取一个 LEB128 无符号整数（不 panic）。
func readUleb(r *protocol.BinaryReader) uint64 {
	var result uint64
	var shift uint
	for {
		b := r.ReadU8()
		result |= uint64(b&0x7F) << shift
		if b&0x80 == 0 {
			return result
		}
		shift += 7
	}
}

// TestSendFakeMonitorJoin_LateJoiner_GetsRegularHint 验证：当用户在对局进行中
// （StatePlaying）加入房间并已被 HandleJoin 标记为 Aborted 时，sendFakeMonitorJoin
// 应发送与普通加入者相同的提示变体（chat-replay-recorder-hint）。迟到加入者的专用
// 提示（chat-late-join-hint）由 ProcessJoinRoom 在 HandleJoin 后单独发送，与本函数解耦。
func TestSendFakeMonitorJoin_LateJoiner_GetsRegularHint(t *testing.T) {
	dir := t.TempDir()
	enabled := true
	cfg := &config.ServerConfig{ReplayEnabled: &enabled, ReplayBaseDir: &dir}
	st := NewServerState(cfg, nil, "test", "", "")
	st.ReplayRecorder = &captureRecorder{}
	hub := NewHub(st, &mockPhira{})
	SetProtocolHackDelay(0)
	t.Cleanup(func() { SetProtocolHackDelay(10 * time.Millisecond) })

	// bob 是迟到加入者：房间已在 StatePlaying，bob.ID 在 Aborted 中。
	bob := NewUser(2, "Bob", "", st)
	bob.SetSession(&mockSession{id: "bob"})
	st.Users[2] = bob
	room := NewRoom("room_late", 1, 8, true) // ReplayEligible=true
	room.State = StatePlaying{
		Results: map[int]config.RecordData{},
		Aborted: map[int]struct{}{2: {}}, // bob 已被 HandleJoin 标记
	}
	bob.Room = room

	before := len(sentTo(bob))
	hub.sendFakeMonitorJoin(bob, room)
	time.Sleep(50 * time.Millisecond)
	sent := sentTo(bob)[before:]

	content, ok := findHintChat(sent)
	if !ok {
		t.Fatal("expected a system chat hint for late joiner")
	}
	systemName := l10n.TL(st.ServerLang, "system-user-name", nil)
	regularHint := l10n.TL(st.ServerLang, "chat-replay-recorder-hint", map[string]string{"name": systemName})
	if content != regularHint {
		t.Errorf("late joiner should receive regular hint variant (late-join hint is sent separately)\n got: %q\nwant: %q", content, regularHint)
	}
}

// TestSendLateJoinHint_SendsLateJoinHint 验证：sendLateJoinHint 异步发送 chat-late-join-hint
// 提示，告知迟到加入者本局已自动计为已放弃、下一局可正常参与。此提示与回放假观战者解耦，
// 即使未启用回放录制也应发送。双保险：提示内容不得包含假观战者说明（应只含迟到加入解释）。
func TestSendLateJoinHint_SendsLateJoinHint(t *testing.T) {
	cfg := &config.ServerConfig{}
	st := NewServerState(cfg, nil, "test", "", "")
	hub := NewHub(st, &mockPhira{})
	SetProtocolHackDelay(0) // 立即异步派发，便于同步验证
	t.Cleanup(func() { SetProtocolHackDelay(10 * time.Millisecond) })

	bob := NewUser(2, "Bob", "", st)
	bob.SetSession(&mockSession{id: "bob"})
	st.Users[2] = bob

	before := len(sentTo(bob))
	hub.sendLateJoinHint(bob, st.ServerLang)
	time.Sleep(30 * time.Millisecond) // 等异步 schedule 派发
	sent := sentTo(bob)[before:]

	content, ok := findHintChat(sent)
	if !ok {
		t.Fatal("expected a system chat hint from sendLateJoinHint")
	}
	lateHint := l10n.TL(st.ServerLang, "chat-late-join-hint", nil)
	if content != lateHint {
		t.Errorf("sendLateJoinHint should send chat-late-join-hint\n got: %q\nwant: %q", content, lateHint)
	}
	// 双保险：迟到加入提示不得包含假观战者说明（即录制器名），否则说明耦合未拆干净。
	recorderName := l10n.TL(st.ServerLang, "replay-recorder-name", nil)
	if strings.Contains(content, recorderName) {
		t.Errorf("chat-late-join-hint should not mention the fake monitor recorder name %q", recorderName)
	}
}

// TestSendFakeMonitorJoin_RegularJoiner_GetsRegularHint 验证：当用户加入非
// StatePlaying 房间（如 SelectChart）时，sendFakeMonitorJoin 应发送默认提示变体
// （chat-replay-recorder-hint），而非迟到变体。
func TestSendFakeMonitorJoin_RegularJoiner_GetsRegularHint(t *testing.T) {
	dir := t.TempDir()
	enabled := true
	cfg := &config.ServerConfig{ReplayEnabled: &enabled, ReplayBaseDir: &dir}
	st := NewServerState(cfg, nil, "test", "", "")
	st.ReplayRecorder = &captureRecorder{}
	hub := NewHub(st, &mockPhira{})
	SetProtocolHackDelay(0)
	t.Cleanup(func() { SetProtocolHackDelay(10 * time.Millisecond) })

	alice := NewUser(1, "Alice", "", st)
	alice.SetSession(&mockSession{id: "alice"})
	st.Users[1] = alice
	room := NewRoom("room_reg", 1, 8, true) // ReplayEligible=true
	room.State = StateSelectChart{}         // 非 StatePlaying → 非迟到加入
	alice.Room = room

	before := len(sentTo(alice))
	hub.sendFakeMonitorJoin(alice, room)
	time.Sleep(50 * time.Millisecond)
	sent := sentTo(alice)[before:]

	content, ok := findHintChat(sent)
	if !ok {
		t.Fatal("expected a system chat hint for regular joiner")
	}
	systemName := l10n.TL(st.ServerLang, "system-user-name", nil)
	regularHint := l10n.TL(st.ServerLang, "chat-replay-recorder-hint", map[string]string{"name": systemName})
	if content != regularHint {
		t.Errorf("regular joiner should receive regular hint variant\n got: %q\nwant: %q", content, regularHint)
	}
}

// ---------- 基准测试 ----------

// BenchmarkDispatch_AllCommands 基准测试所有命令类型的派发吞吐量。
func BenchmarkDispatch_AllCommands(b *testing.B) {
	h := newHarness()
	phira := &mockPhira{
		charts:  map[int]config.Chart{1: {ID: 1, Name: "chart1"}},
		records: map[int]config.RecordData{10: {ID: 10, Player: 1, Score: 900000}},
	}
	hub := NewHub(h.state, phira)
	alice := h.addUser(1, "alice")
	bob := h.addUser(2, "bob")

	// 设置一个进行中的房间用于需要房间上下文的命令
	hub.ProcessClientCommand(alice, protocol.CmdCreateRoom{ID: "bench"})
	hub.ProcessClientCommand(bob, protocol.CmdJoinRoom{ID: "bench", Monitor: false})
	hub.ProcessClientCommand(alice, protocol.CmdSelectChart{ID: 1})
	hub.ProcessClientCommand(alice, protocol.CmdRequestStart{})
	hub.ProcessClientCommand(bob, protocol.CmdReady{})

	cmds := []protocol.ClientCommand{
		protocol.CmdPing{},
		protocol.CmdAuthenticate{Token: "test"},
		protocol.CmdChat{Message: "hi"},
		protocol.CmdCreateRoom{ID: "other"},
		protocol.CmdJoinRoom{ID: "other", Monitor: false},
		protocol.CmdLeaveRoom{},
		protocol.CmdLockRoom{Lock: true},
		protocol.CmdCycleRoom{Cycle: true},
		protocol.CmdSelectChart{ID: 2},
		protocol.CmdRequestStart{},
		protocol.CmdReady{},
		protocol.CmdCancelReady{},
		protocol.CmdPlayed{ID: 10},
		protocol.CmdAbort{},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, cmd := range cmds {
			hub.ProcessClientCommand(alice, cmd)
		}
	}
}
