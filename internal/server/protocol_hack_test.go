package server

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

// captureRecorder 是带调用统计的 fakeRecorder，用于验证 ProtocolHack 是否触发假观战者消息。
type captureRecorder struct {
	fakeRecorder
	mu               sync.Mutex
	fakeMonitorCalls atomic.Int32
	touchesAppended  atomic.Int32
}

func (c *captureRecorder) FakeMonitorInfo(name string) protocol.UserInfo {
	c.fakeMonitorCalls.Add(1)
	return c.fakeRecorder.FakeMonitorInfo(name)
}

// ---------- schedule nil 保护 ----------

func TestProtocolHack_ScheduleNilNoOp(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	ph := hub.NewProtocolHack()
	// 不应 panic
	ph.schedule(nil)
}

// ---------- forceSyncHost nil 保护 ----------

func TestProtocolHack_ForceSyncHost_NilProtection(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	ph := hub.NewProtocolHack()
	// 不应 panic
	ph.forceSyncHost(nil, nil)
	ph.forceSyncHost(nil, h.addUser(1, "alice"))
	ph.forceSyncHost(NewRoom("r", 1, 8, false), nil)
}

// ---------- forceSyncInfo nil 保护 ----------

func TestProtocolHack_ForceSyncInfo_NilProtection(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	ph := hub.NewProtocolHack()
	// 不应 panic
	ph.forceSyncInfo(nil, nil)
	ph.forceSyncInfo(nil, h.addUser(1, "alice"))
	ph.forceSyncInfo(NewRoom("r", 1, 8, false), nil)
}

// ---------- fixClientRoomState 跳过分支 ----------

// TestFixClientRoomState_SkipsSelectChart 验证 SelectChart 状态下不触发补偿
// （客户端已知 SelectChart，无需伪装）。
func TestFixClientRoomState_SkipsSelectChart(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	ph := hub.NewProtocolHack()
	SetProtocolHackDelay(0) // 立即派发，便于同步验证
	t.Cleanup(func() { SetProtocolHackDelay(10 * time.Millisecond) })

	u := h.addUser(1, "alice")
	u.SetSession(&mockSession{id: "alice"})
	r := NewRoom("room1", 1, 8, false)
	r.State = StateSelectChart{}
	r.Chart = &config.Chart{ID: 42, Name: "c"}

	before := len(sentTo(u))
	ph.fixClientRoomState(r, u)
	time.Sleep(30 * time.Millisecond) // 等异步派发
	after := len(sentTo(u))
	// SelectChart 状态应直接跳过，不发任何补偿
	if after != before {
		t.Errorf("SelectChart should skip fixClientRoomState, sent %d commands", after-before)
	}
}

// TestFixClientRoomState_SkipsWhenChartNil 验证 Chart 为 nil 时跳过。
func TestFixClientRoomState_SkipsWhenChartNil(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	ph := hub.NewProtocolHack()
	SetProtocolHackDelay(0)
	t.Cleanup(func() { SetProtocolHackDelay(10 * time.Millisecond) })

	u := h.addUser(1, "alice")
	u.SetSession(&mockSession{id: "alice"})
	r := NewRoom("room1", 1, 8, false)
	// WaitForReady 但 Chart=nil → 跳过
	r.State = StateWaitForReady{Started: map[int]struct{}{}}
	r.Chart = nil

	before := len(sentTo(u))
	ph.fixClientRoomState(r, u)
	time.Sleep(30 * time.Millisecond)
	after := len(sentTo(u))
	if after != before {
		t.Errorf("Chart=nil should skip fixClientRoomState, sent %d commands", after-before)
	}
}

// TestFixClientRoomState_FiresOnWaitForReadyWithChart 验证非 SelectChart 状态 + 有 Chart 时触发补偿。
func TestFixClientRoomState_FiresOnWaitForReadyWithChart(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	ph := hub.NewProtocolHack()
	SetProtocolHackDelay(0)
	t.Cleanup(func() { SetProtocolHackDelay(10 * time.Millisecond) })

	u := h.addUser(1, "alice")
	u.SetSession(&mockSession{id: "alice"})
	r := NewRoom("room1", 1, 8, false)
	r.State = StateWaitForReady{Started: map[int]struct{}{}}
	r.Chart = &config.Chart{ID: 42, Name: "c"}

	before := len(sentTo(u))
	ph.fixClientRoomState(r, u)
	time.Sleep(50 * time.Millisecond) // 等两次延迟派发
	after := len(sentTo(u))
	if after <= before {
		t.Errorf("WaitForReady + Chart should fire fixClientRoomState, sent 0 commands")
	}
	// 应至少看到两条 SrvChangeState（先 SelectChart，再切回 WaitingForReady）
	changeStates := 0
	for _, cmd := range sentTo(u)[before:] {
		if _, ok := cmd.(protocol.SrvChangeState); ok {
			changeStates++
		}
	}
	if changeStates < 2 {
		t.Errorf("expected at least 2 SrvChangeState (select then restore), got %d", changeStates)
	}
}

// ---------- forceSyncInfo 分支 ----------

// TestForceSyncInfo_NonHost_ReceivesChangeHostFalse 验证非房主收到 ChangeHost(false)。
func TestForceSyncInfo_NonHost_ReceivesChangeHostFalse(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	ph := hub.NewProtocolHack()
	SetProtocolHackDelay(0)
	t.Cleanup(func() { SetProtocolHackDelay(10 * time.Millisecond) })

	u := h.addUser(2, "bob") // bob 不是房主
	u.SetSession(&mockSession{id: "bob"})
	r := NewRoom("room1", 1, 8, false) // host=1, 不是 bob
	r.State = StateSelectChart{}
	r.Chart = nil

	before := len(sentTo(u))
	ph.forceSyncInfo(r, u)
	time.Sleep(30 * time.Millisecond)
	sent := sentTo(u)[before:]
	foundChangeHostFalse := false
	for _, cmd := range sent {
		if ch, ok := cmd.(protocol.SrvChangeHost); ok && !ch.IsHost {
			foundChangeHostFalse = true
		}
	}
	if !foundChangeHostFalse {
		t.Error("non-host should receive SrvChangeHost{IsHost:false}")
	}
}

// TestForceSyncInfo_Host_DoesNotReceiveChangeHostFalse 验证房主不会收到 ChangeHost(false)。
func TestForceSyncInfo_Host_DoesNotReceiveChangeHostFalse(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	ph := hub.NewProtocolHack()
	SetProtocolHackDelay(0)
	t.Cleanup(func() { SetProtocolHackDelay(10 * time.Millisecond) })

	host := h.addUser(1, "alice")
	host.SetSession(&mockSession{id: "alice"})
	r := NewRoom("room1", 1, 8, false) // host=1
	r.State = StateSelectChart{}
	r.Chart = nil

	before := len(sentTo(host))
	ph.forceSyncInfo(r, host)
	time.Sleep(30 * time.Millisecond)
	sent := sentTo(host)[before:]
	for _, cmd := range sent {
		if ch, ok := cmd.(protocol.SrvChangeHost); ok && !ch.IsHost {
			t.Error("host should not receive SrvChangeHost{IsHost:false}")
		}
	}
}

// TestForceSyncInfo_NoRecorder_SkipsFakeMonitor 验证 recorder=nil 时跳过假观战者消息。
func TestForceSyncInfo_NoRecorder_SkipsFakeMonitor(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	ph := hub.NewProtocolHack()
	SetProtocolHackDelay(0)
	t.Cleanup(func() { SetProtocolHackDelay(10 * time.Millisecond) })

	// 状态：state.ReplayRecorder 默认为 nil
	u := h.addUser(2, "bob")
	u.SetSession(&mockSession{id: "bob"})
	r := NewRoom("room1", 1, 8, false)
	r.Live = true // 即使 live，recorder 为 nil 也应跳过
	r.State = StateSelectChart{}
	r.Chart = nil

	before := len(sentTo(u))
	ph.forceSyncInfo(r, u)
	time.Sleep(30 * time.Millisecond)
	sent := sentTo(u)[before:]
	// 不应有假观战者的 OnJoinRoom 或 JoinRoom 消息
	for _, cmd := range sent {
		if _, ok := cmd.(protocol.SrvOnJoinRoom); ok {
			t.Error("should not send SrvOnJoinRoom when recorder is nil")
		}
		if sm, ok := cmd.(protocol.SrvMessage); ok {
			if _, ok := sm.Message.(protocol.MsgJoinRoom); ok {
				t.Error("should not send MsgJoinRoom when recorder is nil")
			}
		}
	}
}

// TestForceSyncInfo_NotLive_SkipsFakeMonitor 验证 live=false 时跳过假观战者消息。
func TestForceSyncInfo_NotLive_SkipsFakeMonitor(t *testing.T) {
	h := newHarness()
	rec := &captureRecorder{}
	h.state.ReplayRecorder = rec
	hub := NewHub(h.state, &mockPhira{})
	ph := hub.NewProtocolHack()
	SetProtocolHackDelay(0)
	t.Cleanup(func() { SetProtocolHackDelay(10 * time.Millisecond) })

	u := h.addUser(2, "bob")
	u.SetSession(&mockSession{id: "bob"})
	r := NewRoom("room1", 1, 8, false)
	r.Live = false // 关键：非 live
	r.State = StateSelectChart{}
	r.Chart = nil

	beforeCalls := rec.fakeMonitorCalls.Load()
	ph.forceSyncInfo(r, u)
	time.Sleep(30 * time.Millisecond)
	afterCalls := rec.fakeMonitorCalls.Load()
	if afterCalls != beforeCalls {
		t.Errorf("FakeMonitorInfo should not be called when live=false, calls delta=%d", afterCalls-beforeCalls)
	}
}

// TestForceSyncInfo_LiveWithRecorder_SendsFakeMonitor 验证 live=true + recorder 时发送假观战者 join/leave。
func TestForceSyncInfo_LiveWithRecorder_SendsFakeMonitor(t *testing.T) {
	h := newHarness()
	rec := &captureRecorder{}
	h.state.ReplayRecorder = rec
	hub := NewHub(h.state, &mockPhira{})
	ph := hub.NewProtocolHack()
	SetProtocolHackDelay(0)
	t.Cleanup(func() { SetProtocolHackDelay(10 * time.Millisecond) })

	u := h.addUser(2, "bob")
	u.SetSession(&mockSession{id: "bob"})
	r := NewRoom("room1", 1, 8, false)
	r.Live = true
	r.State = StateSelectChart{}
	r.Chart = nil

	before := len(sentTo(u))
	ph.forceSyncInfo(r, u)
	time.Sleep(50 * time.Millisecond) // 等延迟 leave 派发
	sent := sentTo(u)[before:]
	gotJoin := false
	gotLeave := false
	for _, cmd := range sent {
		if _, ok := cmd.(protocol.SrvOnJoinRoom); ok {
			gotJoin = true
		}
		if sm, ok := cmd.(protocol.SrvMessage); ok {
			if _, ok := sm.Message.(protocol.MsgJoinRoom); ok {
				gotJoin = true
			}
			if _, ok := sm.Message.(protocol.MsgLeaveRoom); ok {
				gotLeave = true
			}
		}
	}
	if !gotJoin {
		t.Error("expected fake monitor join messages (OnJoinRoom or MsgJoinRoom)")
	}
	if !gotLeave {
		t.Error("expected delayed fake monitor leave message (MsgLeaveRoom)")
	}
}

// ---------- SetProtocolHackDelay(0) 立即异步派发 ----------

// TestSetProtocolHackDelay_ZeroDispatchesImmediately 验证 delay=0 时通过 go 派发而非 AfterFunc。
// 这是个语义测试：delay=0 时 fn 应在合理时间内执行（异步）。
func TestSetProtocolHackDelay_ZeroDispatchesImmediately(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	ph := hub.NewProtocolHack()
	SetProtocolHackDelay(0)
	t.Cleanup(func() { SetProtocolHackDelay(10 * time.Millisecond) })

	done := make(chan struct{})
	ph.schedule(func() { close(done) })
	select {
	case <-done:
		// ok
	case <-time.After(200 * time.Millisecond):
		t.Error("delay=0 should dispatch fn within 200ms")
	}
}

// TestSetProtocolHackDelay_PositiveDelaysExecution 验证 delay>0 时延迟执行。
func TestSetProtocolHackDelay_PositiveDelaysExecution(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	SetProtocolHackDelay(50 * time.Millisecond)
	t.Cleanup(func() { SetProtocolHackDelay(10 * time.Millisecond) })
	ph := hub.NewProtocolHack()

	start := time.Now()
	done := make(chan struct{})
	ph.schedule(func() { close(done) })
	select {
	case <-done:
		elapsed := time.Since(start)
		if elapsed < 30*time.Millisecond {
			t.Errorf("delay=50ms should defer execution, but ran in %v", elapsed)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("delayed fn should execute within 500ms")
	}
}

// ---------- forceSyncHost 主流程 ----------

// TestForceSyncHost_NonHostSendsChangeHostFalse 验证非房主用户收到 IsHost=false。
func TestForceSyncHost_NonHostSendsChangeHostFalse(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	ph := hub.NewProtocolHack()
	SetProtocolHackDelay(0)
	t.Cleanup(func() { SetProtocolHackDelay(10 * time.Millisecond) })

	bob := h.addUser(2, "bob")
	bob.SetSession(&mockSession{id: "bob"})
	r := NewRoom("room1", 1, 8, false) // host=1, bob 非房主

	before := len(sentTo(bob))
	ph.forceSyncHost(r, bob)
	time.Sleep(30 * time.Millisecond)
	sent := sentTo(bob)[before:]
	found := false
	for _, cmd := range sent {
		if ch, ok := cmd.(protocol.SrvChangeHost); ok && !ch.IsHost {
			found = true
		}
	}
	if !found {
		t.Error("non-host should receive SrvChangeHost{IsHost:false}")
	}
}

// TestForceSyncHost_HostSendsChangeHostTrue 验证房主收到 IsHost=true。
func TestForceSyncHost_HostSendsChangeHostTrue(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	ph := hub.NewProtocolHack()
	SetProtocolHackDelay(0)
	t.Cleanup(func() { SetProtocolHackDelay(10 * time.Millisecond) })

	host := h.addUser(1, "alice")
	host.SetSession(&mockSession{id: "alice"})
	r := NewRoom("room1", 1, 8, false) // alice 是房主

	before := len(sentTo(host))
	ph.forceSyncHost(r, host)
	time.Sleep(30 * time.Millisecond)
	sent := sentTo(host)[before:]
	found := false
	for _, cmd := range sent {
		if ch, ok := cmd.(protocol.SrvChangeHost); ok && ch.IsHost {
			found = true
		}
	}
	if !found {
		t.Error("host should receive SrvChangeHost{IsHost:true}")
	}
}

// ---------- 回放录制器提示聊天 ----------

// findHintChat 在 sent 中查找系统聊天（MsgChat，不限 User）。
// 返回 (内容, 是否找到)。
func findHintChat(sent []protocol.ServerCommand) (string, bool) {
	for _, cmd := range sent {
		if sm, ok := cmd.(protocol.SrvMessage); ok {
			if chat, ok := sm.Message.(protocol.MsgChat); ok {
				return chat.Content, true
			}
		}
	}
	return "", false
}

// TestForceSyncInfo_LiveWithRecorder_SendsHintChat 验证 live=true + recorder 时
// 在派发假观战者加入消息后，紧跟一条系统聊天（User=0），明确告知玩家这是服务器模拟的
// 回放采集会话、无需理会。提示文本应包含录制器显示名以便玩家对应。
func TestForceSyncInfo_LiveWithRecorder_SendsHintChat(t *testing.T) {
	h := newHarness()
	rec := &captureRecorder{}
	h.state.ReplayRecorder = rec
	hub := NewHub(h.state, &mockPhira{})
	ph := hub.NewProtocolHack()
	SetProtocolHackDelay(0)
	t.Cleanup(func() { SetProtocolHackDelay(10 * time.Millisecond) })

	u := h.addUser(2, "bob")
	u.SetSession(&mockSession{id: "bob"})
	r := NewRoom("room1", 1, 8, false)
	r.Live = true
	r.State = StateSelectChart{}
	r.Chart = nil

	before := len(sentTo(u))
	ph.forceSyncInfo(r, u)
	time.Sleep(50 * time.Millisecond)
	sent := sentTo(u)[before:]

	content, ok := findHintChat(sent)
	if !ok {
		t.Fatal("expected a system chat (MsgChat User=0) hint after fake monitor join")
	}
	expectedHint := l10n.TL(h.state.ServerLang, "chat-replay-recorder-hint", nil)
	if content != expectedHint {
		t.Errorf("hint chat should match chat-replay-recorder-hint\n got: %q\nwant: %q", content, expectedHint)
	}
}

// TestForceSyncInfo_NoRecorder_SkipsHintChat 验证 recorder=nil 时既不发假观战者消息，
// 也不发提示聊天——避免给玩家发送与回放无关的误导性提示。
func TestForceSyncInfo_NoRecorder_SkipsHintChat(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	ph := hub.NewProtocolHack()
	SetProtocolHackDelay(0)
	t.Cleanup(func() { SetProtocolHackDelay(10 * time.Millisecond) })

	u := h.addUser(2, "bob")
	u.SetSession(&mockSession{id: "bob"})
	r := NewRoom("room1", 1, 8, false)
	r.Live = true
	r.State = StateSelectChart{}
	r.Chart = nil

	before := len(sentTo(u))
	ph.forceSyncInfo(r, u)
	time.Sleep(30 * time.Millisecond)
	sent := sentTo(u)[before:]
	if _, ok := findHintChat(sent); ok {
		t.Error("should not send hint chat when recorder is nil")
	}
}

// TestForceSyncInfo_NotLive_SkipsHintChat 验证 live=false 时也不发提示聊天。
func TestForceSyncInfo_NotLive_SkipsHintChat(t *testing.T) {
	h := newHarness()
	rec := &captureRecorder{}
	h.state.ReplayRecorder = rec
	hub := NewHub(h.state, &mockPhira{})
	ph := hub.NewProtocolHack()
	SetProtocolHackDelay(0)
	t.Cleanup(func() { SetProtocolHackDelay(10 * time.Millisecond) })

	u := h.addUser(2, "bob")
	u.SetSession(&mockSession{id: "bob"})
	r := NewRoom("room1", 1, 8, false)
	r.Live = false
	r.State = StateSelectChart{}
	r.Chart = nil

	before := len(sentTo(u))
	ph.forceSyncInfo(r, u)
	time.Sleep(30 * time.Millisecond)
	sent := sentTo(u)[before:]
	if _, ok := findHintChat(sent); ok {
		t.Error("should not send hint chat when room is not live")
	}
}
