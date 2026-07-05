package server

import (
	"testing"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

// withShortCountdown 临时把倒计时缩短到测试可控范围，返回恢复函数。
func withShortCountdown(total time.Duration, reminders []time.Duration) func() {
	oldDur, oldRem := readyCountdownDuration, readyCountdownReminders
	readyCountdownDuration = total
	readyCountdownReminders = reminders
	return func() {
		readyCountdownDuration = oldDur
		readyCountdownReminders = oldRem
	}
}

// TestReadyCountdown_ForcedStart_AbortsUnready 验证 60 秒倒计时到期后强制开赛，
// 未准备玩家被标记为 Aborted（本局不能参与），已准备的房主不进 Aborted。
func TestReadyCountdown_ForcedStart_AbortsUnready(t *testing.T) {
	h := newHarness()
	phira := &mockPhira{charts: map[int]config.Chart{1: {ID: 1, Name: "c"}}}
	hub := NewHub(h.state, phira)
	host := h.addUser(1, "host")
	player := h.addUser(2, "player") // 未准备

	cleanup := withShortCountdown(100*time.Millisecond, nil)
	defer cleanup()

	hub.mustDispatch(t, host, protocol.CmdCreateRoom{ID: "room1"})
	hub.mustDispatch(t, player, protocol.CmdJoinRoom{ID: "room1", Monitor: false})
	hub.mustDispatch(t, host, protocol.CmdSelectChart{ID: 1})
	hub.mustDispatch(t, host, protocol.CmdRequestStart{})

	time.Sleep(200 * time.Millisecond) // 等待强制开赛

	room := h.room("room1")
	room.Mu.Lock()
	st, ok := room.State.(StatePlaying)
	room.Mu.Unlock()
	if !ok {
		t.Fatalf("room should be Playing after countdown, got %T", room.State)
	}
	if _, aborted := st.Aborted[player.ID]; !aborted {
		t.Error("unready player should be in Aborted after forced start")
	}
	if _, aborted := st.Aborted[host.ID]; aborted {
		t.Error("host (ready) should not be in Aborted")
	}
}

// TestReadyCountdown_CancelOnHostCancel 验证房主取消游戏后倒计时被取消，
// 不会在到点后强制开赛。
func TestReadyCountdown_CancelOnHostCancel(t *testing.T) {
	h := newHarness()
	phira := &mockPhira{charts: map[int]config.Chart{1: {ID: 1, Name: "c"}}}
	hub := NewHub(h.state, phira)
	host := h.addUser(1, "host")
	player := h.addUser(2, "player") // 未准备，保证 RequestStart 后仍在 WaitForReady

	cleanup := withShortCountdown(100*time.Millisecond, nil)
	defer cleanup()

	hub.mustDispatch(t, host, protocol.CmdCreateRoom{ID: "room1"})
	hub.mustDispatch(t, player, protocol.CmdJoinRoom{ID: "room1", Monitor: false})
	hub.mustDispatch(t, host, protocol.CmdSelectChart{ID: 1})
	hub.mustDispatch(t, host, protocol.CmdRequestStart{})

	// 房主取消游戏 → 回到 SelectChart，倒计时应被取消。
	hub.mustDispatch(t, host, protocol.CmdCancelReady{})

	time.Sleep(200 * time.Millisecond) // 原本会强制开赛的时间点

	room := h.room("room1")
	room.Mu.Lock()
	_, playing := room.State.(StatePlaying)
	room.Mu.Unlock()
	if playing {
		t.Error("room should not be force-started after host cancelled the game")
	}
}

// TestReadyCountdown_ContestManualStart_NoCountdown 验证比赛房（ManualStart）
// 不启动倒计时，到点后不会强制开赛。
func TestReadyCountdown_ContestManualStart_NoCountdown(t *testing.T) {
	h := newHarness()
	phira := &mockPhira{charts: map[int]config.Chart{1: {ID: 1, Name: "c"}}}
	hub := NewHub(h.state, phira)
	host := h.addUser(1, "host")

	cleanup := withShortCountdown(100*time.Millisecond, nil)
	defer cleanup()

	hub.mustDispatch(t, host, protocol.CmdCreateRoom{ID: "room1"})
	hub.mustDispatch(t, host, protocol.CmdSelectChart{ID: 1})
	room := h.room("room1")
	hub.EnableContest(room, nil) // ManualStart=true

	hub.mustDispatch(t, host, protocol.CmdRequestStart{})

	time.Sleep(200 * time.Millisecond)

	room.Mu.Lock()
	_, playing := room.State.(StatePlaying)
	room.Mu.Unlock()
	if playing {
		t.Error("contest room (ManualStart) should not be force-started by countdown")
	}
}

// TestReadyCountdown_RemindersBroadcast 验证倒计时提醒在到点时以系统身份广播到房间成员。
func TestReadyCountdown_RemindersBroadcast(t *testing.T) {
	h := newHarness()
	phira := &mockPhira{charts: map[int]config.Chart{1: {ID: 1, Name: "c"}}}
	hub := NewHub(h.state, phira)
	host := h.addUser(1, "host")
	player := h.addUser(2, "player") // 未准备，保证不会全员就绪触发 startPlaying

	// 总时长 200ms，剩 1 秒时提醒——但 1s > 200ms，所以用 100ms（int(100ms/1s)=0）。
	// 测试只验证有系统聊天发出，不验证具体秒数文本。
	cleanup := withShortCountdown(200*time.Millisecond, []time.Duration{100 * time.Millisecond})
	defer cleanup()

	hub.mustDispatch(t, host, protocol.CmdCreateRoom{ID: "room1"})
	hub.mustDispatch(t, player, protocol.CmdJoinRoom{ID: "room1", Monitor: false})
	hub.mustDispatch(t, host, protocol.CmdSelectChart{ID: 1})

	hostBefore := len(sentTo(host))
	hub.mustDispatch(t, host, protocol.CmdRequestStart{})
	time.Sleep(150 * time.Millisecond) // 等 100ms 提醒点

	sysID := h.state.SystemChatUserID()
	found := false
	for _, cmd := range sentTo(host)[hostBefore:] {
		sm, ok := cmd.(protocol.SrvMessage)
		if !ok {
			continue
		}
		chat, ok := sm.Message.(protocol.MsgChat)
		if !ok {
			continue
		}
		if chat.User == sysID && chat.Content != "" {
			// 内容应是 chat-ready-countdown 渲染结果（含"0 秒"）
			expected := l10n.TL(h.state.ServerLang, "chat-ready-countdown", map[string]string{"seconds": "0"})
			if chat.Content == expected {
				found = true
			}
		}
	}
	if !found {
		t.Error("host should receive countdown reminder chat from system user")
	}
}

// hasAbortFor 报告 cmds 中是否包含指定用户 ID 的 MsgAbort 广播。
func hasAbortFor(cmds []protocol.ServerCommand, userID int) bool {
	for _, cmd := range cmds {
		sm, ok := cmd.(protocol.SrvMessage)
		if !ok {
			continue
		}
		if abort, ok := sm.Message.(protocol.MsgAbort); ok && int(abort.User) == userID {
			return true
		}
	}
	return false
}

// TestReadyCountdown_ForcedStart_UnreadyBackToSelectChart 验证强制开赛后未准备玩家
// 被送回 SelectChart 状态（变观战者，跳过本轮），不收到 MsgStartPlaying 或 MsgAbort。
func TestReadyCountdown_ForcedStart_UnreadyBackToSelectChart(t *testing.T) {
	h := newHarness()
	phira := &mockPhira{charts: map[int]config.Chart{1: {ID: 1, Name: "c"}}}
	hub := NewHub(h.state, phira)
	host := h.addUser(1, "host")
	player := h.addUser(2, "player") // 未准备

	cleanup := withShortCountdown(80*time.Millisecond, nil)
	defer cleanup()

	hub.mustDispatch(t, host, protocol.CmdCreateRoom{ID: "room1"})
	hub.mustDispatch(t, player, protocol.CmdJoinRoom{ID: "room1", Monitor: false})
	hub.mustDispatch(t, host, protocol.CmdSelectChart{ID: 1})

	playerBefore := len(sentTo(player))
	hub.mustDispatch(t, host, protocol.CmdRequestStart{})

	// 等待强制开赛（80ms）+ 一点余量。
	time.Sleep(120 * time.Millisecond)

	// 未准备玩家不应收到 MsgStartPlaying。
	if hasMsgStartPlaying(sentTo(player)[playerBefore:]) {
		t.Error("unready player should NOT receive MsgStartPlaying")
	}
	// 未准备玩家不应收到 MsgAbort。
	if hasAbortFor(sentTo(player)[playerBefore:], player.ID) {
		t.Error("unready player should NOT receive MsgAbort")
	}
	// 未准备玩家应收到 SrvChangeState(SelectChart)，回到选谱界面。
	if !hasStateSelectChart(sentTo(player)[playerBefore:]) {
		t.Error("unready player should receive SrvChangeState(SelectChart) to go back to chart select")
	}
}

// hasMsgStartPlaying 报告 cmds 中是否包含 MsgStartPlaying 广播。
func hasMsgStartPlaying(cmds []protocol.ServerCommand) bool {
	for _, cmd := range cmds {
		sm, ok := cmd.(protocol.SrvMessage)
		if !ok {
			continue
		}
		if _, ok := sm.Message.(protocol.MsgStartPlaying); ok {
			return true
		}
	}
	return false
}

// hasStateSelectChart 报告 cmds 中是否包含 SrvChangeState(SelectChart)。
func hasStateSelectChart(cmds []protocol.ServerCommand) bool {
	for _, cmd := range cmds {
		sc, ok := cmd.(protocol.SrvChangeState)
		if !ok {
			continue
		}
		if _, ok := sc.State.(protocol.RoomStateSelectChart); ok {
			return true
		}
	}
	return false
}

// TestReadyCountdown_SinglePlayerSkipsCountdown 验证单人房 RequestStart 后立即进入
// Playing，不启动「准备倒计时」——避免无谓调度 6 个 timer 然后立刻被 cancel 掉。
func TestReadyCountdown_SinglePlayerSkipsCountdown(t *testing.T) {
	h := newHarness()
	phira := &mockPhira{charts: map[int]config.Chart{1: {ID: 1, Name: "c"}}}
	hub := NewHub(h.state, phira)
	host := h.addUser(1, "host")

	cleanup := withShortCountdown(80*time.Millisecond, nil)
	defer cleanup()

	hub.mustDispatch(t, host, protocol.CmdCreateRoom{ID: "room1"})
	hub.mustDispatch(t, host, protocol.CmdSelectChart{ID: 1})
	hub.mustDispatch(t, host, protocol.CmdRequestStart{})

	room := h.room("room1")
	room.Mu.Lock()
	_, ok := room.State.(StatePlaying)
	cancelled := room.readyCancel.Load() == nil
	room.Mu.Unlock()
	if !ok {
		t.Fatalf("single-player room should enter Playing immediately, got %T", room.State)
	}
	if !cancelled {
		t.Error("single-player room should not start ready countdown (state already advanced to Playing)")
	}
}
