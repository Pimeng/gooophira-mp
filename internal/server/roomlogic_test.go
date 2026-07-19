package server

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

// ---------- 测试桩 ----------

// mockSession 是用于测试的最小 Session 实现，记录收到的命令。
type mockSession struct {
	id   string
	mu   sync.Mutex
	sent []protocol.ServerCommand
}

func (m *mockSession) ID() string { return m.id }
func (m *mockSession) TrySend(cmd protocol.ServerCommand) {
	m.mu.Lock()
	m.sent = append(m.sent, cmd)
	m.mu.Unlock()
}
func (m *mockSession) TrySendFrame(frame []byte) {
	// 把预编码的帧解码回 ServerCommand，加到 sent 列表（测试断言用）。
	res := protocol.TryDecodeFrame(frame, 0)
	if res.Kind != protocol.FrameOK {
		return
	}
	cmd, err := protocol.DecodePacket(res.Payload, protocol.DecodeServerCommand)
	if err != nil || cmd == nil {
		return
	}
	m.mu.Lock()
	m.sent = append(m.sent, cmd)
	m.mu.Unlock()
}
func (m *mockSession) TrySendFrameOwned(frame []byte) { m.TrySendFrame(frame) }
func (m *mockSession) Close()                         {}

// testHarness 持有共享用户表与捕获的广播，便于断言。
type testHarness struct {
	users      map[int]*User
	broadcasts []protocol.ServerCommand
	state      *ServerState
}

func newHarness(monitors ...int) *testHarness {
	cfg := &config.ServerConfig{Monitors: monitors}
	st := NewServerState(cfg, nil, "test", "", "")
	return &testHarness{users: map[int]*User{}, state: st}
}

func (h *testHarness) addUser(id int, name string) *User {
	u := NewUser(id, name, "", h.state)
	u.SetSession(&mockSession{id: name})
	h.users[id] = u
	h.state.Users[id] = u // Hub 广播经 state.Users 查会话
	return u
}

// sentTo 返回某用户 mock 会话收到的全部命令的副本。
func sentTo(u *User) []protocol.ServerCommand {
	ms, ok := u.Session().(*mockSession)
	if !ok {
		return nil
	}
	ms.mu.Lock()
	defer ms.mu.Unlock()
	return append([]protocol.ServerCommand(nil), ms.sent...)
}

func (h *testHarness) lifecycle() *RoomLifecycle {
	return &RoomLifecycle{
		UsersByID:           func(id int) *User { return h.users[id] },
		Broadcast:           func(cmd protocol.ServerCommand) { h.broadcasts = append(h.broadcasts, cmd) },
		BroadcastToMonitors: func(cmd protocol.ServerCommand) {},
		PickNextHostID: func(ids []int, oldHostID int) (int, bool) {
			return pickNextHost(ids, oldHostID)
		},
		Lang:             h.state.ServerLang,
		SystemChatUserID: h.state.SystemChatUserID,
	}
}

// countMsg 统计广播中 SrvMessage 内某具体 Message 类型出现次数。
func countMsg[T protocol.Message](h *testHarness) int {
	n := 0
	for _, c := range h.broadcasts {
		if sm, ok := c.(protocol.SrvMessage); ok {
			if _, ok := sm.Message.(T); ok {
				n++
			}
		}
	}
	return n
}

func lastChat(h *testHarness) (protocol.MsgChat, bool) {
	var last protocol.MsgChat
	found := false
	for _, c := range h.broadcasts {
		if sm, ok := c.(protocol.SrvMessage); ok {
			if chat, ok := sm.Message.(protocol.MsgChat); ok {
				last, found = chat, true
			}
		}
	}
	return last, found
}

// ---------- 添加用户 ----------

func TestAddUser_MaxUsersAndMonitors(t *testing.T) {
	h := newHarness(200)
	r := NewRoom("room1", 1, 2, false) // 房主为 1，人数上限为 2。
	u2 := h.addUser(2, "bob")
	u3 := h.addUser(3, "carol")

	if !r.AddUser(u2, false) {
		t.Fatal("second player should fit (max=2)")
	}
	if r.AddUser(u3, false) {
		t.Fatal("third player should be rejected (max=2)")
	}
	if r.UserCount() != 2 {
		t.Fatalf("user count = %d, want 2", r.UserCount())
	}
	// 观战者不受 maxUsers 限制
	mon := h.addUser(200, "mon")
	if !r.AddUser(mon, true) {
		t.Fatal("monitor should always be addable")
	}
	if r.MonitorCount() != 1 {
		t.Fatalf("monitor count = %d, want 1", r.MonitorCount())
	}
}

// ---------- 校验 ----------

func TestValidateStart(t *testing.T) {
	h := newHarness()
	host := h.addUser(1, "alice")
	other := h.addUser(2, "bob")
	r := NewRoom("room1", 1, 8, false)

	if err := r.ValidateStart(other); err != ErrOnlyHost {
		t.Errorf("non-host start should be ErrOnlyHost, got %v", err)
	}
	if err := r.ValidateStart(host); err != ErrNoChartSelected {
		t.Errorf("no chart should be ErrNoChartSelected, got %v", err)
	}
	r.Chart = &config.Chart{ID: 1, Name: "c"}
	if err := r.ValidateStart(host); err != nil {
		t.Errorf("host with chart in SelectChart should pass, got %v", err)
	}
	r.State = StateWaitForReady{Started: map[int]struct{}{}}
	if err := r.ValidateStart(host); err != ErrRoomInvalidState {
		t.Errorf("start in non-SelectChart should be ErrRoomInvalidState, got %v", err)
	}
}

func TestValidateJoin(t *testing.T) {
	h := newHarness(200)
	r := NewRoom("room1", 1, 8, false)
	player := h.addUser(2, "bob")
	mon := h.addUser(200, "mon")
	nonMon := h.addUser(3, "carol")

	// 锁定
	r.Locked = true
	if err := r.ValidateJoin(player, false); err != ErrJoinRoomLocked {
		t.Errorf("locked join should be ErrJoinRoomLocked, got %v", err)
	}
	r.Locked = false

	// 游戏等待就绪时普通玩家也可加入（WaitForReady 是预游戏状态）
	r.State = StateWaitForReady{Started: map[int]struct{}{}}
	if err := r.ValidateJoin(player, false); err != nil {
		t.Errorf("player join during WaitForReady should pass, got %v", err)
	}
	// 观战者可以
	if err := r.ValidateJoin(mon, true); err != nil {
		t.Errorf("monitor join during WaitForReady should pass, got %v", err)
	}
	// 无观战权限者请求观战被拒
	if err := r.ValidateJoin(nonMon, true); err != ErrJoinCantMonitor {
		t.Errorf("non-monitor monitor-join should be ErrJoinCantMonitor, got %v", err)
	}
}

func TestValidateJoin_ContestWhitelist(t *testing.T) {
	h := newHarness()
	r := NewRoom("room1", 1, 8, false)
	r.Contest = &Contest{Whitelist: map[int]struct{}{1: {}}}
	allowed := h.addUser(1, "alice")
	denied := h.addUser(2, "bob")
	if err := r.ValidateJoin(denied, false); err != ErrNotWhitelisted {
		t.Errorf("non-whitelisted join should be ErrNotWhitelisted, got %v", err)
	}
	if err := r.ValidateJoin(allowed, false); err != nil {
		t.Errorf("whitelisted join should pass, got %v", err)
	}
}

// ---------- 状态机：就绪 → 游戏中 ----------

func TestCheckAllReady_WaitToPlaying(t *testing.T) {
	h := newHarness()
	r := NewRoom("room1", 1, 8, false)
	r.Chart = &config.Chart{ID: 1, Name: "c"}
	host := h.addUser(1, "alice")
	bob := h.addUser(2, "bob")
	r.AddUser(bob, false)
	_ = host

	// 仅 host 就绪 → 不应转换
	r.State = StateWaitForReady{Started: map[int]struct{}{1: {}}}
	r.CheckAllReady(h.lifecycle())
	if _, ok := r.State.(StateWaitForReady); !ok {
		t.Fatal("should stay WaitForReady when not all ready")
	}

	// 全员就绪 → 转 Playing，广播 StartPlaying + ChangeState
	r.State = StateWaitForReady{Started: map[int]struct{}{1: {}, 2: {}}}
	h.broadcasts = nil
	r.CheckAllReady(h.lifecycle())
	if _, ok := r.State.(StatePlaying); !ok {
		t.Fatalf("should transition to Playing, got %T", r.State)
	}
	if countMsg[protocol.MsgStartPlaying](h) != 1 {
		t.Error("should broadcast StartPlaying once")
	}
	// gameTime 应被重置为 -Inf
	if !math.IsInf(bob.GameTime(), -1) {
		t.Errorf("bob.GameTime should reset to -Inf, got %v", bob.GameTime())
	}
}

func TestCheckAllReady_ContestManualStartBlocks(t *testing.T) {
	h := newHarness()
	r := NewRoom("room1", 1, 8, false)
	r.Contest = &Contest{Whitelist: map[int]struct{}{1: {}}, ManualStart: true}
	r.State = StateWaitForReady{Started: map[int]struct{}{1: {}}}
	h.addUser(1, "alice")
	r.CheckAllReady(h.lifecycle())
	if _, ok := r.State.(StateWaitForReady); !ok {
		t.Fatal("contest manual_start should block auto-transition to Playing")
	}
}

// ---------- 状态机：游戏中 → 结算 ----------

func TestCheckAllReady_PlayingToSelectChart(t *testing.T) {
	h := newHarness()
	r := NewRoom("room1", 1, 8, false)
	host := h.addUser(1, "alice")
	bob := h.addUser(2, "bob")
	r.AddUser(bob, false)
	_ = host

	results := map[int]config.RecordData{
		1: {Score: 900000, Accuracy: 0.95, Std: ptr64(0.030)},
		2: {Score: 980000, Accuracy: 0.99, Std: ptr64(0.010)},
	}
	r.State = StatePlaying{Results: results, Aborted: map[int]struct{}{}}
	r.CheckAllReady(h.lifecycle())

	if _, ok := r.State.(StateSelectChart); !ok {
		t.Fatalf("should return to SelectChart after all finished, got %T", r.State)
	}
	if countMsg[protocol.MsgGameEnd](h) != 1 {
		t.Error("should broadcast GameEnd once")
	}
	// 结算摘要应作为系统聊天广播（user 0）
	if chat, ok := lastChat(h); !ok || chat.User != 0 {
		t.Errorf("expected system summary chat, got %+v ok=%v", chat, ok)
	}
}

func TestCheckAllReady_PlayingNotFinished(t *testing.T) {
	h := newHarness()
	r := NewRoom("room1", 1, 8, false)
	bob := h.addUser(2, "bob")
	h.addUser(1, "alice")
	r.AddUser(bob, false)

	// 只有 host 交了成绩，bob 未完成（且在线）→ 不结算
	r.State = StatePlaying{Results: map[int]config.RecordData{1: {Score: 1}}, Aborted: map[int]struct{}{}}
	r.CheckAllReady(h.lifecycle())
	if _, ok := r.State.(StatePlaying); !ok {
		t.Fatal("should stay Playing when a player is unfinished")
	}
}

// TestCheckAllReady_ContestAutoDisband_NoDeadlock 是 DisbandRoom 自死锁修复的回归测试。
// 旧实现：checkPlaying 在持 room.Mu 时调 lc.DisbandRoom，而 DisbandRoom 内部又 room.Mu.Lock()
// → 自死锁。新实现：checkPlaying 返回 disband=true，不直接调 DisbandRoom。
// 此测试用 lifecycle 不设置 DisbandRoom 字段（即 nil），若旧实现复现会因 nil 调用 panic，
// 新实现则正常返回 disband=true。
func TestCheckAllReady_ContestAutoDisband_NoDeadlock(t *testing.T) {
	h := newHarness()
	r := NewRoom("room1", 1, 8, false)
	r.Contest = &Contest{Whitelist: map[int]struct{}{1: {}, 2: {}}, ManualStart: true, AutoDisband: true}
	bob := h.addUser(2, "bob")
	alice := h.addUser(1, "alice")
	r.AddUser(bob, false)
	r.AddUser(alice, false)

	// 双方均 finished（alice 交成绩，bob 中止）
	r.State = StatePlaying{
		Results: map[int]config.RecordData{1: {Score: 900000, Accuracy: 0.95, Std: ptr64(0.030)}},
		Aborted: map[int]struct{}{2: {}},
	}

	// 在持 room.Mu 的情况下调 OnUserLeave（模拟 removeUser/CmdLeaveRoom 的真实路径）。
	// 旧实现会在 checkPlaying → lc.DisbandRoom → room.Mu.Lock() 自死锁，测试会卡死或超时。
	r.Mu.Lock()
	shouldDrop, disband := r.OnUserLeave(h.lifecycle(), alice)
	r.Mu.Unlock()

	if shouldDrop {
		t.Error("shouldDrop=false expected: bob still in room")
	}
	if !disband {
		t.Fatal("disband=true expected: Contest.AutoDisband triggered")
	}
	// 房间状态应已切回 SelectChart（checkPlaying 内已切换）
	if _, ok := r.State.(StateSelectChart); !ok {
		t.Fatalf("state should be SelectChart after game end, got %T", r.State)
	}
	// 应广播 GameEnd
	if countMsg[protocol.MsgGameEnd](h) != 1 {
		t.Error("should broadcast GameEnd once")
	}
}

// ---------- 房主转移 ----------

func TestOnUserLeave_HostTransfer(t *testing.T) {
	h := newHarness()
	r := NewRoom("room1", 1, 8, false)
	host := h.addUser(1, "alice")
	bob := h.addUser(2, "bob")
	host.Room, bob.Room = r, r
	r.AddUser(bob, false)

	disband, _ := r.OnUserLeave(h.lifecycle(), host)
	if disband {
		t.Fatal("room should not disband while bob remains")
	}
	if r.HostID != 2 {
		t.Errorf("host should transfer to bob(2), got %d", r.HostID)
	}
	if countMsg[protocol.MsgNewHost](h) != 1 {
		t.Error("should broadcast NewHost once")
	}
	// 新房主应收到 ChangeHost(is_host=true)
	found := false
	for _, c := range sentTo(bob) {
		if ch, ok := c.(protocol.SrvChangeHost); ok && ch.IsHost {
			found = true
		}
	}
	if !found {
		t.Error("new host should receive ChangeHost{IsHost:true}")
	}
}

func TestOnUserLeave_LastUserDisbands(t *testing.T) {
	h := newHarness()
	r := NewRoom("room1", 1, 8, false)
	host := h.addUser(1, "alice")
	host.Room = r
	shouldDrop, _ := r.OnUserLeave(h.lifecycle(), host)
	if !shouldDrop {
		t.Fatal("room should disband when last user leaves")
	}
}

// ---------- cycle 房主轮换 ----------

func TestCheckAllReady_CycleRotatesHost(t *testing.T) {
	h := newHarness()
	r := NewRoom("room1", 1, 8, false)
	r.Cycle = true
	host := h.addUser(1, "alice")
	bob := h.addUser(2, "bob")
	r.AddUser(bob, false)
	_ = host

	// 一局结束（双方都交成绩）→ host 从 1 轮到 2
	r.State = StatePlaying{
		Results: map[int]config.RecordData{1: {Score: 1}, 2: {Score: 2}},
		Aborted: map[int]struct{}{},
	}
	r.CheckAllReady(h.lifecycle())
	if r.HostID != 2 {
		t.Errorf("cycle should rotate host 1→2, got %d", r.HostID)
	}
}

// TestRotateCycleHost_DesignatedHost 验证管理员指定的下一轮房主会被采用。
func TestRotateCycleHost_DesignatedHost(t *testing.T) {
	h := newHarness()
	r := NewRoom("room1", 1, 8, false)
	r.Cycle = true
	h.addUser(1, "alice")
	bob := h.addUser(2, "bob")
	carol := h.addUser(3, "carol")
	r.AddUser(bob, false)
	r.AddUser(carol, false)

	// 指定 carol 为下一轮房主。默认轮换（pickNextHost）会从 1→2，但指定应覆盖为 3。
	r.SetNextHost(3)
	if _, ok := r.NextHostID(); !ok {
		t.Fatal("NextHostID should report set")
	}
	r.State = StatePlaying{
		Results: map[int]config.RecordData{1: {Score: 1}, 2: {Score: 2}, 3: {Score: 3}},
		Aborted: map[int]struct{}{},
	}
	r.CheckAllReady(h.lifecycle())
	if r.HostID != 3 {
		t.Errorf("designated host should win, got %d", r.HostID)
	}
	// 一次性消费
	if _, ok := r.NextHostID(); ok {
		t.Error("NextHostID should be cleared after rotateCycleHost")
	}
}

// TestRotateCycleHost_DesignatedHostLeftRoom 验证指定用户已离开时回退到默认轮换。
func TestRotateCycleHost_DesignatedHostLeftRoom(t *testing.T) {
	h := newHarness()
	r := NewRoom("room1", 1, 8, false)
	r.Cycle = true
	h.addUser(1, "alice")
	bob := h.addUser(2, "bob")
	r.AddUser(bob, false)

	// 指定 99（不在房间内）→ 应回退到默认 1→2。
	r.SetNextHost(99)
	r.State = StatePlaying{
		Results: map[int]config.RecordData{1: {Score: 1}, 2: {Score: 2}},
		Aborted: map[int]struct{}{},
	}
	r.CheckAllReady(h.lifecycle())
	if r.HostID != 2 {
		t.Errorf("should fall back to default rotation, got %d", r.HostID)
	}
	if _, ok := r.NextHostID(); ok {
		t.Error("NextHostID should be cleared even on fallback")
	}
}

// TestRotateCycleHost_DesignatedHostIsCurrent 验证指定当前房主时回退到默认轮换。
func TestRotateCycleHost_DesignatedHostIsCurrent(t *testing.T) {
	h := newHarness()
	r := NewRoom("room1", 1, 8, false)
	r.Cycle = true
	h.addUser(1, "alice")
	bob := h.addUser(2, "bob")
	r.AddUser(bob, false)

	// 指定当前房主 1 → 应回退到默认 1→2（cycle 模式必须轮换）。
	r.SetNextHost(1)
	r.State = StatePlaying{
		Results: map[int]config.RecordData{1: {Score: 1}, 2: {Score: 2}},
		Aborted: map[int]struct{}{},
	}
	r.CheckAllReady(h.lifecycle())
	if r.HostID != 2 {
		t.Errorf("should fall back to default rotation when designated==current, got %d", r.HostID)
	}
}

// ---------- 游戏中加入自动计入已完成 ----------

func TestHandleJoin_DuringPlayingMarksAborted(t *testing.T) {
	h := newHarness()
	r := NewRoom("room1", 1, 8, false)
	r.State = StatePlaying{Results: map[int]config.RecordData{}, Aborted: map[int]struct{}{}}
	late := h.addUser(2, "bob")
	r.HandleJoin(h.lifecycle(), late)
	st := r.State.(StatePlaying)
	if _, ok := st.Aborted[2]; !ok {
		t.Error("late-joining player during Playing should be marked aborted")
	}
}

// ---------- 游戏排名 ----------

// TestBroadcastGameRanking_SinglePlayerSkipped 验证单人游玩（仅一份成绩）不输出排名。
func TestBroadcastGameRanking_SinglePlayerSkipped(t *testing.T) {
	h := newHarness()
	r := NewRoom("room1", 1, 8, false)
	h.addUser(1, "alice")
	std := 0.012
	r.State = StatePlaying{
		Results:   map[int]config.RecordData{1: {Score: 950000, Accuracy: 0.95, Std: &std}},
		Aborted:   map[int]struct{}{},
		StartedAt: time.Now(),
	}
	r.broadcastGameRanking(h.lifecycle(), r.State.(StatePlaying))
	if _, ok := lastChat(h); ok {
		t.Error("single-player game should not broadcast ranking")
	}
}

// TestBroadcastGameRanking_MultiPlayerOrderedByScore 验证多人按分数降序排名
// （平局取 id 升序），含 Std 时输出误差段，格式与示例一致。
func TestBroadcastGameRanking_MultiPlayerOrderedByScore(t *testing.T) {
	h := newHarness()
	r := NewRoom("room1", 1, 8, false)
	h.addUser(1, "alice")
	h.addUser(2, "bob")
	std1, std2 := 0.002, 0.019
	r.State = StatePlaying{
		Results: map[int]config.RecordData{
			2: {Score: 970000, Accuracy: 0.97, Std: &std2},
			1: {Score: 1000000, Accuracy: 1.0, Std: &std1},
		},
		Aborted:   map[int]struct{}{},
		StartedAt: time.Now(),
	}
	r.broadcastGameRanking(h.lifecycle(), r.State.(StatePlaying))
	chat, ok := lastChat(h)
	if !ok {
		t.Fatal("expected ranking chat to be broadcast")
	}
	// 默认 ServerLang=zh-CN；故意乱序写入 Results 验证按分数降序排列。
	// 前导 \n 用于在聊天中与上方消息视觉分隔。
	want := "\n" + strings.Repeat("=", 72) + "\n" +
		"本轮排名\n" +
		"1. alice - 分数：1000000，准度：100%，误差：±2ms\n" +
		"2. bob - 分数：970000，准度：97%，误差：±19ms"
	if chat.Content != want {
		t.Errorf("ranking = %q, want %q", chat.Content, want)
	}
}

// TestBroadcastGameRanking_NoStdOmitsErrorSegment 验证所有玩家 Std==nil 时
// 排名行不含误差段。
func TestBroadcastGameRanking_NoStdOmitsErrorSegment(t *testing.T) {
	h := newHarness()
	r := NewRoom("room1", 1, 8, false)
	h.addUser(1, "alice")
	h.addUser(2, "bob")
	r.State = StatePlaying{
		Results: map[int]config.RecordData{
			1: {Score: 1000000, Accuracy: 1.0, Std: nil},
			2: {Score: 970000, Accuracy: 0.97, Std: nil},
		},
		Aborted:   map[int]struct{}{},
		StartedAt: time.Now(),
	}
	r.broadcastGameRanking(h.lifecycle(), r.State.(StatePlaying))
	chat, ok := lastChat(h)
	if !ok {
		t.Fatal("expected ranking chat to be broadcast")
	}
	if strings.Contains(chat.Content, "误差") {
		t.Errorf("ranking should not contain 误差 when Std==nil, got %q", chat.Content)
	}
	if !strings.Contains(chat.Content, "1. alice - 分数：1000000，准度：100%\n") {
		t.Errorf("alice line should lack 误差 segment, got %q", chat.Content)
	}
}

// ---------- 客户端状态 ----------

func TestClientState(t *testing.T) {
	h := newHarness()
	r := NewRoom("room1", 1, 8, false)
	host := h.addUser(1, "alice")
	bob := h.addUser(2, "bob")
	r.AddUser(bob, false)
	r.State = StateWaitForReady{Started: map[int]struct{}{2: {}}}

	cs := r.ClientState(host, h.lifecycle().UsersByID)
	if !cs.IsHost {
		t.Error("alice should be host")
	}
	if cs.IsReady {
		t.Error("alice not in started set → not ready")
	}
	if len(cs.Users) != 2 {
		t.Errorf("client state should list 2 users, got %d", len(cs.Users))
	}
	bobState := r.ClientState(bob, h.lifecycle().UsersByID)
	if bobState.IsHost {
		t.Error("bob is not host")
	}
	if !bobState.IsReady {
		t.Error("bob is in started set → ready")
	}
}

// ---------- 基准测试 ----------

// benchDispatch 是 mustDispatch 的 testing.B 版本。
func benchDispatch(b *testing.B, h *Hub, user *User, cmd protocol.ClientCommand) protocol.ServerCommand {
	b.Helper()
	// 与生产代码对齐：仅房间命令（Touches/Judges/Played）持 room.Mu，
	// 其余命令持 state.Mu（全局串行，保护 state.Rooms 等全局 map）。
	switch cmd.(type) {
	case protocol.CmdTouches, protocol.CmdJudges, protocol.CmdPlayed:
		if room := user.Room; room != nil {
			room.Mu.Lock()
			resp, ok := h.ProcessClientCommand(user, cmd)
			room.Mu.Unlock()
			if !ok {
				b.Fatalf("expected a response for %T", cmd)
			}
			return resp
		}
	}
	h.State.Mu.Lock()
	resp, ok := h.ProcessClientCommand(user, cmd)
	h.State.Mu.Unlock()
	if !ok {
		b.Fatalf("expected a response for %T", cmd)
	}
	return resp
}

// BenchmarkRoomLifecycle_N 基准测试完整房间生命周期，调整玩家数 N。
// 运行: go test -bench=BenchmarkRoomLifecycle -benchmem ./internal/server/

func BenchmarkRoomLifecycle_4Players(b *testing.B)  { benchmarkRoomLifecycle(b, 4) }
func BenchmarkRoomLifecycle_8Players(b *testing.B)  { benchmarkRoomLifecycle(b, 8) }
func BenchmarkRoomLifecycle_16Players(b *testing.B) { benchmarkRoomLifecycle(b, 16) }

func benchmarkRoomLifecycle(b *testing.B, n int) {
	h := newHarness()
	phira := &mockPhira{
		charts: map[int]config.Chart{1: {ID: 1, Name: "chart1"}},
		records: map[int]config.RecordData{
			10: {ID: 10, Player: 1, Score: 900000, Accuracy: 0.95, Std: ptr64(0.02)},
		},
	}
	hub := NewHub(h.state, phira)

	// 创建玩家
	players := make([]*User, n)
	for i := range n {
		id := i + 1
		players[i] = h.addUser(id, fmt.Sprintf("player%d", id))
	}

	// 为所有玩家预配置相同的 record，使 CmdPlayed 通过校验
	for i := 2; i <= n; i++ {
		phira.records[i*10] = config.RecordData{ID: i * 10, Player: i, Score: 900000, Accuracy: 0.95, Std: ptr64(0.02)}
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		// 重置状态（每次迭代独立）。state.Rooms 受 state.Mu 约束，须持锁替换。
		h.state.Mu.Lock()
		h.state.Rooms = make(map[protocol.RoomID]*Room)
		for _, u := range players {
			u.Room = nil
		}
		h.state.Mu.Unlock()

		// 1) 创建房间
		benchDispatch(b, hub, players[0], protocol.CmdCreateRoom{ID: "bench"})

		// 2) 其余玩家加入
		for i := 1; i < n; i++ {
			benchDispatch(b, hub, players[i], protocol.CmdJoinRoom{ID: "bench", Monitor: false})
		}

		// 3) 选谱
		benchDispatch(b, hub, players[0], protocol.CmdSelectChart{ID: 1})

		// 4) 请求开始
		benchDispatch(b, hub, players[0], protocol.CmdRequestStart{})

		// 5) 其余玩家准备
		for i := 1; i < n; i++ {
			benchDispatch(b, hub, players[i], protocol.CmdReady{})
		}

		// 6) 所有玩家提交成绩
		for i := range n {
			benchDispatch(b, hub, players[i], protocol.CmdPlayed{ID: int32((i + 1) * 10)})
		}
	}
}

// ptr64 把 float64 字面量转为指针，便于填充 RecordData.Std / StdScore 之类的 *float64 字段。
// 定义见 dispatch_test.go（同一 package，所有 _test.go 文件共享）。

// BenchmarkRoomGameplay 基准测试 Playing 阶段的高频帧（Touches / Judges）。
func BenchmarkRoomGameplay(b *testing.B) {
	h := newHarness()
	phira := &mockPhira{
		charts:  map[int]config.Chart{1: {ID: 1, Name: "chart1"}},
		records: map[int]config.RecordData{10: {ID: 10, Player: 1, Score: 900000}},
	}
	hub := NewHub(h.state, phira)
	alice := h.addUser(1, "alice")
	bob := h.addUser(2, "bob")

	// 进入 Playing 状态
	benchDispatch(b, hub, alice, protocol.CmdCreateRoom{ID: "room1"})
	benchDispatch(b, hub, bob, protocol.CmdJoinRoom{ID: "room1", Monitor: false})
	benchDispatch(b, hub, alice, protocol.CmdSelectChart{ID: 1})
	benchDispatch(b, hub, alice, protocol.CmdRequestStart{})
	benchDispatch(b, hub, bob, protocol.CmdReady{})

	// 准备触摸帧和判定事件
	touches := protocol.CmdTouches{
		Frames: []protocol.TouchFrame{
			{Time: 0.0, Points: []protocol.TouchPoint{
				{ID: 0, Pos: protocol.CompactPos{X: 0.5, Y: 0.3}},
				{ID: 1, Pos: protocol.CompactPos{X: 0.7, Y: 0.4}},
			}},
		},
	}
	judges := protocol.CmdJudges{
		Judges: []protocol.JudgeEvent{
			{Time: 1.0, LineID: 0, NoteID: 10, Judgement: protocol.JudgePerfect},
			{Time: 2.0, LineID: 1, NoteID: 20, Judgement: protocol.JudgeGood},
		},
	}

	b.ReportAllocs()
	b.ResetTimer()

	room := alice.Room
	for b.Loop() {
		// Touches/Judges 是仅房间命令，生产代码持 room.Mu 派发。
		room.Mu.Lock()
		hub.ProcessClientCommand(alice, touches)
		room.Mu.Unlock()
		room.Mu.Lock()
		hub.ProcessClientCommand(bob, touches)
		room.Mu.Unlock()
		room.Mu.Lock()
		hub.ProcessClientCommand(alice, judges)
		room.Mu.Unlock()
		room.Mu.Lock()
		hub.ProcessClientCommand(bob, judges)
		room.Mu.Unlock()
	}
}

// BenchmarkRoomCreateMany 基准测试创建大量房间和快速加入。
func BenchmarkRoomCreateMany(b *testing.B) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		// 每次迭代先清理。state.Rooms 受 state.Mu 约束，须持锁替换。
		h.state.Mu.Lock()
		h.state.Rooms = make(map[protocol.RoomID]*Room)
		h.state.Mu.Unlock()

		// 创建 N=100 个房间，每个 1 个用户
		for j := range 100 {
			u := h.addUser(j+1, fmt.Sprintf("u%d", j+1))
			benchDispatch(b, hub, u, protocol.CmdCreateRoom{ID: protocol.RoomID(fmt.Sprintf("r%d", j))})
		}
	}
}
