package server

import (
	"strconv"
	"testing"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

// withShortPlayDeadline 临时把结算超时缩短到测试可控范围，返回恢复函数。
func withShortPlayDeadline(d time.Duration) func() {
	old := playDeadlineDuration
	playDeadlineDuration = d
	return func() { playDeadlineDuration = old }
}

// hasMsgGameEnd 报告 cmds 中是否包含 MsgGameEnd 广播。
func hasMsgGameEnd(cmds []protocol.ServerCommand) bool {
	for _, cmd := range cmds {
		sm, ok := cmd.(protocol.SrvMessage)
		if !ok {
			continue
		}
		if _, ok := sm.Message.(protocol.MsgGameEnd); ok {
			return true
		}
	}
	return false
}

// hasChatContent 报告 cmds 中是否包含指定内容的 MsgChat 广播。
func hasChatContent(cmds []protocol.ServerCommand, content string) bool {
	for _, cmd := range cmds {
		sm, ok := cmd.(protocol.SrvMessage)
		if !ok {
			continue
		}
		chat, ok := sm.Message.(protocol.MsgChat)
		if !ok {
			continue
		}
		if chat.Content == content {
			return true
		}
	}
	return false
}

// setupPlayingRoom 创建一个双人房间并进入 Playing 状态，返回 hub 和两个用户。
func setupPlayingRoom(t *testing.T, h *testHarness, hub *Hub) (*User, *User, *Room) {
	t.Helper()
	alice := h.addUser(1, "alice")
	bob := h.addUser(2, "bob")
	hub.mustDispatch(t, alice, protocol.CmdCreateRoom{ID: "room1"})
	hub.mustDispatch(t, bob, protocol.CmdJoinRoom{ID: "room1", Monitor: false})
	hub.mustDispatch(t, alice, protocol.CmdSelectChart{ID: 1})
	hub.mustDispatch(t, alice, protocol.CmdRequestStart{})
	hub.mustDispatch(t, bob, protocol.CmdReady{})
	room := h.room("room1")
	if _, ok := room.State.(StatePlaying); !ok {
		t.Fatalf("room should be Playing, got %T", room.State)
	}
	return alice, bob, room
}

// TestPlayDeadline_ForceEnds 验证 120s 倒计时到点后，未结算玩家被标记为 Aborted，
// 本局强制结束，房间回到 SelectChart，并广播 MsgAbort/MsgGameEnd/系统聊天通知。
func TestPlayDeadline_ForceEnds(t *testing.T) {
	h := newHarness()
	phira := &mockPhira{
		charts:  map[int]config.Chart{1: {ID: 1, Name: "c"}},
		records: map[int]config.RecordData{10: {ID: 10, Player: 1, Score: 900000, Accuracy: 0.95, Std: ptr64(0.02)}},
	}
	hub := NewHub(h.state, phira)

	cleanup := withShortPlayDeadline(100 * time.Millisecond)
	defer cleanup()

	alice, bob, room := setupPlayingRoom(t, h, hub)

	aliceBefore := len(sentTo(alice))
	// alice 提交成绩 → 首位结算者，启动结算超时倒计时
	hub.mustDispatch(t, alice, protocol.CmdPlayed{ID: 10})

	// 确认倒计时已启动
	if room.playDeadlineCancel.Load() == nil {
		t.Fatal("play deadline should be started after first result")
	}

	time.Sleep(250 * time.Millisecond) // 等待倒计时到点

	// 房间应回到 SelectChart
	room.Mu.Lock()
	_, isSelect := room.State.(StateSelectChart)
	room.Mu.Unlock()
	if !isSelect {
		t.Fatalf("room should be SelectChart after deadline, got %T", room.State)
	}

	// 倒计时已被清除
	if room.playDeadlineCancel.Load() != nil {
		t.Error("play deadline should be cancelled after game end")
	}

	// alice 应收到 bob 的 MsgAbort
	aliceCmds := sentTo(alice)[aliceBefore:]
	if !hasAbortFor(aliceCmds, bob.ID) {
		t.Error("alice should receive MsgAbort for bob (force-aborted)")
	}
	// alice 应收到 MsgGameEnd
	if !hasMsgGameEnd(aliceCmds) {
		t.Error("alice should receive MsgGameEnd")
	}
	// alice 应收到系统聊天通知（chat-play-deadline）
	secs := strconv.Itoa(int(playDeadlineDuration / time.Second))
	expectedChat := l10n.TL(h.state.ServerLang, "chat-play-deadline", map[string]string{"seconds": secs})
	if expectedChat != "" && expectedChat != "chat-play-deadline" {
		if !hasChatContent(aliceCmds, expectedChat) {
			t.Errorf("alice should receive chat-play-deadline notification, expected %q", expectedChat)
		}
	}
}

// TestPlayDeadline_CancelledOnNormalEnd 验证所有玩家正常提交后倒计时被取消，
// 不会在到点后误触发强制结束。
func TestPlayDeadline_CancelledOnNormalEnd(t *testing.T) {
	h := newHarness()
	phira := &mockPhira{
		charts: map[int]config.Chart{1: {ID: 1, Name: "c"}},
		records: map[int]config.RecordData{
			10: {ID: 10, Player: 1, Score: 900000, Accuracy: 0.95, Std: ptr64(0.02)},
			20: {ID: 20, Player: 2, Score: 980000, Accuracy: 0.99, Std: ptr64(0.01)},
		},
	}
	hub := NewHub(h.state, phira)

	cleanup := withShortPlayDeadline(100 * time.Millisecond)
	defer cleanup()

	alice, _, room := setupPlayingRoom(t, h, hub)

	// alice 提交 → 启动倒计时
	hub.mustDispatch(t, alice, protocol.CmdPlayed{ID: 10})
	if room.playDeadlineCancel.Load() == nil {
		t.Fatal("play deadline should be started after first result")
	}

	// bob 立即提交 → 正常结算 → 倒计时被取消
	bob := h.users[2]
	hub.mustDispatch(t, bob, protocol.CmdPlayed{ID: 20})

	// 房间应回到 SelectChart
	room.Mu.Lock()
	_, isSelect := room.State.(StateSelectChart)
	room.Mu.Unlock()
	if !isSelect {
		t.Fatalf("room should be SelectChart after normal end, got %T", room.State)
	}
	// 倒计时应被取消
	if room.playDeadlineCancel.Load() != nil {
		t.Error("play deadline should be cancelled after normal game end")
	}

	time.Sleep(200 * time.Millisecond) // 原本会到点的时间

	// 确认没有额外的状态变化（仍在 SelectChart）
	room.Mu.Lock()
	_, stillSelect := room.State.(StateSelectChart)
	room.Mu.Unlock()
	if !stillSelect {
		t.Error("room should still be SelectChart (no stale deadline fire)")
	}
}

// TestPlayDeadline_ContestRoomNoDeadline 验证比赛房不启动结算超时倒计时。
func TestPlayDeadline_ContestRoomNoDeadline(t *testing.T) {
	h := newHarness()
	phira := &mockPhira{
		charts:  map[int]config.Chart{1: {ID: 1, Name: "c"}},
		records: map[int]config.RecordData{10: {ID: 10, Player: 1, Score: 900000, Accuracy: 0.95, Std: ptr64(0.02)}},
	}
	hub := NewHub(h.state, phira)

	cleanup := withShortPlayDeadline(100 * time.Millisecond)
	defer cleanup()

	alice := h.addUser(1, "alice")
	hub.mustDispatch(t, alice, protocol.CmdCreateRoom{ID: "room1"})
	hub.mustDispatch(t, alice, protocol.CmdSelectChart{ID: 1})
	room := h.room("room1")
	hub.EnableContest(room, nil) // ManualStart=true, AutoDisband=true

	hub.mustDispatch(t, alice, protocol.CmdRequestStart{})
	if err := hub.StartContest(room, true); err != nil {
		t.Fatalf("StartContest failed: %v", err)
	}

	// alice 提交 → 比赛房不应启动倒计时
	hub.mustDispatch(t, alice, protocol.CmdPlayed{ID: 10})
	if room.playDeadlineCancel.Load() != nil {
		t.Error("contest room should not start play deadline")
	}

	time.Sleep(200 * time.Millisecond)
	// 比赛房 AutoDisband：单人提交后 CheckAllReady → checkPlaying → AutoDisband → 房间已解散。
	// DisbandRoom 由 handlePlayed 异步执行（goroutine 持 state.Mu 删 state.Rooms），
	// 读取 state.Rooms 须持 state.Mu 避免与异步删除竞争。
	h.state.Mu.Lock()
	disbanded := h.room("room1") == nil
	h.state.Mu.Unlock()
	if !disbanded {
		t.Error("contest room should be disbanded after auto-disband")
	}
}

// TestPlayDeadline_SinglePlayerNoDeadline 验证单人房提交后立即结算，不启动倒计时。
func TestPlayDeadline_SinglePlayerNoDeadline(t *testing.T) {
	h := newHarness()
	phira := &mockPhira{
		charts:  map[int]config.Chart{1: {ID: 1, Name: "c"}},
		records: map[int]config.RecordData{10: {ID: 10, Player: 1, Score: 900000, Accuracy: 0.95, Std: ptr64(0.02)}},
	}
	hub := NewHub(h.state, phira)

	cleanup := withShortPlayDeadline(100 * time.Millisecond)
	defer cleanup()

	alice := h.addUser(1, "alice")
	hub.mustDispatch(t, alice, protocol.CmdCreateRoom{ID: "room1"})
	hub.mustDispatch(t, alice, protocol.CmdSelectChart{ID: 1})
	hub.mustDispatch(t, alice, protocol.CmdRequestStart{})
	// 单人房 → 立即 Playing
	room := h.room("room1")
	if _, ok := room.State.(StatePlaying); !ok {
		t.Fatalf("single-player room should be Playing, got %T", room.State)
	}

	hub.mustDispatch(t, alice, protocol.CmdPlayed{ID: 10})
	// 单人提交 → 立即结算 → 不启动倒计时
	if room.playDeadlineCancel.Load() != nil {
		t.Error("single-player room should not start play deadline (game ends immediately)")
	}
	room.Mu.Lock()
	_, isSelect := room.State.(StateSelectChart)
	room.Mu.Unlock()
	if !isSelect {
		t.Fatalf("single-player room should be SelectChart after play, got %T", room.State)
	}
}

// TestPlayDeadline_CancelledOnDisband 验证房间解散后倒计时被取消，不会误触发。
func TestPlayDeadline_CancelledOnDisband(t *testing.T) {
	h := newHarness()
	phira := &mockPhira{
		charts:  map[int]config.Chart{1: {ID: 1, Name: "c"}},
		records: map[int]config.RecordData{10: {ID: 10, Player: 1, Score: 900000, Accuracy: 0.95, Std: ptr64(0.02)}},
	}
	hub := NewHub(h.state, phira)

	cleanup := withShortPlayDeadline(100 * time.Millisecond)
	defer cleanup()

	alice, _, room := setupPlayingRoom(t, h, hub)
	hub.mustDispatch(t, alice, protocol.CmdPlayed{ID: 10})
	if room.playDeadlineCancel.Load() == nil {
		t.Fatal("play deadline should be started after first result")
	}

	// 解散房间 → 倒计时应被取消
	h.state.Mu.Lock()
	hub.DisbandRoom(room)
	h.state.Mu.Unlock()

	if room.playDeadlineCancel.Load() != nil {
		t.Error("play deadline should be cancelled after disband")
	}
	if h.room("room1") != nil {
		t.Error("room should be removed after disband")
	}

	time.Sleep(200 * time.Millisecond) // 原本会到点的时间
	// 无 panic / 无额外状态变化即通过
}
