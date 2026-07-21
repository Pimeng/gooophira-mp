package server

import (
	"strings"
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/common/protocol"
	"github.com/Pimeng/gooophira-mp/internal/config"
)

// ---------- AddLog 边界 ----------

// TestAddLog_TruncatesLongMessage 验证超过 maxLogMessageLen(1000) 的消息被截断并加省略号。
func TestAddLog_TruncatesLongMessage(t *testing.T) {
	r := NewRoom("room1", 1, 8, false)
	long := strings.Repeat("a", maxLogMessageLen+50)
	r.AddLog(long, 1000)
	logs := r.GetRecentLogs()
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	msg := logs[0].Message
	// 截断后长度应为 maxLogMessageLen + len(roomLogTrailEllip)
	wantLen := maxLogMessageLen + len(roomLogTrailEllip)
	if len(msg) != wantLen {
		t.Errorf("truncated length = %d, want %d", len(msg), wantLen)
	}
	if !strings.HasSuffix(msg, roomLogTrailEllip) {
		t.Errorf("truncated message should end with %q, got %q", roomLogTrailEllip, msg[len(msg)-len(roomLogTrailEllip):])
	}
}

// TestAddLog_KeepsExactBoundaryLength 验证刚好等于 maxLogMessageLen 的消息不截断。
func TestAddLog_KeepsExactBoundaryLength(t *testing.T) {
	r := NewRoom("room1", 1, 8, false)
	exact := strings.Repeat("b", maxLogMessageLen)
	r.AddLog(exact, 1000)
	logs := r.GetRecentLogs()
	if len(logs[0].Message) != maxLogMessageLen {
		t.Errorf("exact-boundary message should not be truncated, got len=%d", len(logs[0].Message))
	}
	if strings.HasSuffix(logs[0].Message, roomLogTrailEllip) {
		t.Error("exact-boundary message should not have ellipsis suffix")
	}
}

// TestAddLog_FIFOCap 验证超过 maxRecentLogs(50) 条时按 FIFO 截断，保留最新 50 条。
func TestAddLog_FIFOCap(t *testing.T) {
	r := NewRoom("room1", 1, 8, false)
	// 写入 60 条
	for i := 0; i < 60; i++ {
		r.AddLog(string(rune('A'+i%26))+string(rune('0'+i/26)), int64(i))
	}
	logs := r.GetRecentLogs()
	if len(logs) != maxRecentLogs {
		t.Fatalf("expected %d logs, got %d", maxRecentLogs, len(logs))
	}
	// 应保留最后 50 条（timestamp 10..59）
	if logs[0].Timestamp != 10 {
		t.Errorf("oldest kept timestamp = %d, want 10 (FIFO evicted 0..9)", logs[0].Timestamp)
	}
	if logs[len(logs)-1].Timestamp != 59 {
		t.Errorf("newest timestamp = %d, want 59", logs[len(logs)-1].Timestamp)
	}
}

// TestAddLog_EmptyMessage 验证空字符串消息也能正常记录。
func TestAddLog_EmptyMessage(t *testing.T) {
	r := NewRoom("room1", 1, 8, false)
	r.AddLog("", 1000)
	logs := r.GetRecentLogs()
	if len(logs) != 1 || logs[0].Message != "" {
		t.Errorf("empty message should be recorded as-is, got %+v", logs)
	}
}

// TestGetRecentLogs_ReturnsCopy 验证 GetRecentLogs 返回副本，修改不影响内部状态。
func TestGetRecentLogs_ReturnsCopy(t *testing.T) {
	r := NewRoom("room1", 1, 8, false)
	r.AddLog("first", 1)
	r.AddLog("second", 2)
	logs := r.GetRecentLogs()
	logs[0] = RoomLog{Message: "tampered", Timestamp: 999}
	again := r.GetRecentLogs()
	if again[0].Message == "tampered" {
		t.Error("GetRecentLogs should return a copy, but modifying it affected internal state")
	}
}

// ---------- AddUser 重复添加 ----------

// TestAddUser_DuplicatePlayerNotReAdded 验证重复添加同一玩家不会重复入列。
func TestAddUser_DuplicatePlayerNotReAdded(t *testing.T) {
	h := newHarness()
	r := NewRoom("room1", 1, 8, false)
	bob := h.addUser(2, "bob")
	if !r.AddUser(bob, false) {
		t.Fatal("first add should succeed")
	}
	if !r.AddUser(bob, false) {
		t.Fatal("duplicate add should still return true (idempotent)")
	}
	if r.UserCount() != 2 { // 房主 1 加上玩家 2。
		t.Errorf("duplicate add should not increase count, got %d", r.UserCount())
	}
	// users 列表不应有重复
	ids := r.UserIDs()
	count := 0
	for _, id := range ids {
		if id == 2 {
			count++
		}
	}
	if count != 1 {
		t.Errorf("bob should appear once in users, got %d", count)
	}
}

// TestAddUser_DuplicateMonitorNotReAdded 验证重复添加同一观战者不会重复入列。
func TestAddUser_DuplicateMonitorNotReAdded(t *testing.T) {
	h := newHarness(200)
	r := NewRoom("room1", 1, 8, false)
	mon := h.addUser(200, "mon")
	if !r.AddUser(mon, true) {
		t.Fatal("first monitor add should succeed")
	}
	if !r.AddUser(mon, true) {
		t.Fatal("duplicate monitor add should still return true")
	}
	if r.MonitorCount() != 1 {
		t.Errorf("duplicate monitor should not increase count, got %d", r.MonitorCount())
	}
}

// TestAddUser_PlayerAtMaxReturnsFalse 验证玩家满员时返回 false。
func TestAddUser_PlayerAtMaxReturnsFalse(t *testing.T) {
	h := newHarness()
	// max=1：建房后 host 已占满
	r := NewRoom("room1", 1, 1, false)
	bob := h.addUser(2, "bob")
	if r.AddUser(bob, false) {
		t.Error("add should fail when room is at max users")
	}
}

// ---------- RefreshLive 各种组合 ----------

func TestRefreshLive_Combinations(t *testing.T) {
	cases := []struct {
		name           string
		monitors       int
		replayEnabled  bool
		replayEligible bool
		wantLive       bool
	}{
		{"no monitors, no replay", 0, false, false, false},
		{"no monitors, replay disabled", 0, false, true, false},
		{"no monitors, replay enabled, eligible", 0, true, true, true},
		{"no monitors, replay enabled, ineligible", 0, true, false, false},
		{"has monitors, no replay", 1, false, false, true},
		{"has monitors, replay enabled", 1, true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness()
			r := NewRoom("room1", 1, 8, tc.replayEligible)
			for i := 0; i < tc.monitors; i++ {
				mon := h.addUser(100+i, "mon")
				r.AddUser(mon, true)
			}
			got := r.RefreshLive(tc.replayEnabled)
			if got != tc.wantLive {
				t.Errorf("RefreshLive = %v, want %v", got, tc.wantLive)
			}
			if r.Live != tc.wantLive {
				t.Errorf("r.Live = %v, want %v", r.Live, tc.wantLive)
			}
		})
	}
}

// ---------- AllParticipantIDs 顺序 ----------

func TestAllParticipantIDs_Order(t *testing.T) {
	h := newHarness(200)
	r := NewRoom("room1", 1, 8, false) // 用户列表仅包含 1。
	bob := h.addUser(2, "bob")
	carol := h.addUser(3, "carol")
	mon := h.addUser(200, "mon")

	r.AddUser(bob, false)
	r.AddUser(mon, true)
	r.AddUser(carol, false)

	ids := r.AllParticipantIDs()
	// 期望顺序：玩家在前（按加入顺序：1,2,3），观战者在后（200）
	want := []int{1, 2, 3, 200}
	if len(ids) != len(want) {
		t.Fatalf("len = %d, want %d", len(ids), len(want))
	}
	for i, w := range want {
		if ids[i] != w {
			t.Errorf("ids[%d] = %d, want %d (full: %v)", i, ids[i], w, ids)
		}
	}
}

// TestAllParticipantIDs_ReturnsCopy 验证返回副本，修改不影响内部状态。
func TestAllParticipantIDs_ReturnsCopy(t *testing.T) {
	r := NewRoom("room1", 1, 8, false)
	ids := r.AllParticipantIDs()
	if len(ids) == 0 {
		t.Fatal("expected at least host id")
	}
	ids[0] = 9999
	again := r.AllParticipantIDs()
	if again[0] == 9999 {
		t.Error("AllParticipantIDs should return a copy")
	}
}

// ---------- ClientRoomState 各状态 ----------

func TestClientRoomState_States(t *testing.T) {
	r := NewRoom("room1", 1, 8, false)

	// 初始：SelectChart，无 Chart → ID 为 nil
	r.State = StateSelectChart{}
	r.Chart = nil
	st := r.ClientRoomState()
	if sc, ok := st.(protocol.RoomStateSelectChart); !ok || sc.ID != nil {
		t.Errorf("SelectChart no chart: want RoomStateSelectChart{nil}, got %+v", st)
	}

	// SelectChart + Chart → ID 指向 chart id
	r.Chart = &config.Chart{ID: 42, Name: "c"}
	st = r.ClientRoomState()
	if sc, ok := st.(protocol.RoomStateSelectChart); !ok || sc.ID == nil || *sc.ID != 42 {
		t.Errorf("SelectChart with chart: want RoomStateSelectChart{ID=42}, got %+v", st)
	}

	// 等待准备状态。
	r.State = StateWaitForReady{Started: map[int]struct{}{}}
	st = r.ClientRoomState()
	if _, ok := st.(protocol.RoomStateWaitingForReady); !ok {
		t.Errorf("WaitForReady: want RoomStateWaitingForReady, got %T", st)
	}

	// 游戏进行状态。
	r.State = StatePlaying{Results: map[int]config.RecordData{}, Aborted: map[int]struct{}{}}
	st = r.ClientRoomState()
	if _, ok := st.(protocol.RoomStatePlaying); !ok {
		t.Errorf("Playing: want RoomStatePlaying, got %T", st)
	}
}

// ---------- 房间状态标志读取 ----------

func TestRoom_FlagGetters(t *testing.T) {
	r := NewRoom("room1", 1, 8, false)
	// NewRoom 把 host 加入 users，所以 IsEmpty=false（仍有人）
	if r.IsEmpty() {
		t.Error("new room with host should not be empty")
	}
	if r.IsLocked() {
		t.Error("new room should not be locked")
	}
	if r.IsCycle() {
		t.Error("new room should not be cycle")
	}
	if r.IsLive() {
		t.Error("new room with no monitors / replay should not be live")
	}
	r.Locked = true
	r.Cycle = true
	r.Live = true
	if !r.IsLocked() || !r.IsCycle() || !r.IsLive() {
		t.Error("flags should reflect set values")
	}
}

// ---------- 房主检查 ----------

func TestCheckHost(t *testing.T) {
	h := newHarness()
	r := NewRoom("room1", 1, 8, false)
	host := h.addUser(1, "alice")
	other := h.addUser(2, "bob")
	if err := r.CheckHost(host); err != nil {
		t.Errorf("host should pass CheckHost, got %v", err)
	}
	if err := r.CheckHost(other); err != ErrOnlyHost {
		t.Errorf("non-host should get ErrOnlyHost, got %v", err)
	}
}

func TestIsHost_NilUser(t *testing.T) {
	r := NewRoom("room1", 1, 8, false)
	if r.IsHost(nil) {
		t.Error("IsHost(nil) should return false")
	}
}

// ---------- NewRoom 默认状态 ----------

func TestNewRoom_Defaults(t *testing.T) {
	r := NewRoom("room1", 7, 16, true)
	if r.ID != "room1" {
		t.Errorf("ID = %q, want room1", r.ID)
	}
	if r.HostID != 7 {
		t.Errorf("HostID = %d, want 7", r.HostID)
	}
	if r.MaxUsers != 16 {
		t.Errorf("MaxUsers = %d, want 16", r.MaxUsers)
	}
	if !r.ReplayEligible {
		t.Error("ReplayEligible should be true")
	}
	if _, ok := r.State.(StateSelectChart); !ok {
		t.Errorf("initial state should be SelectChart, got %T", r.State)
	}
	if r.UserCount() != 1 {
		t.Errorf("new room should have 1 user (host), got %d", r.UserCount())
	}
	if r.MonitorCount() != 0 {
		t.Errorf("new room should have 0 monitors, got %d", r.MonitorCount())
	}
}

// ---------- removeInt 工具函数 ----------

func TestRemoveInt(t *testing.T) {
	cases := []struct {
		s      []int
		v      int
		want   []int
		wantLn int
	}{
		{[]int{1, 2, 3}, 2, []int{1, 3}, 2},
		{[]int{1, 2, 3}, 1, []int{2, 3}, 2},
		{[]int{1, 2, 3}, 3, []int{1, 2}, 2},
		{[]int{1, 2, 3}, 99, []int{1, 2, 3}, 3}, // 不存在：原样返回
		{[]int{}, 1, []int{}, 0},                // 空切片
	}
	for _, tc := range cases {
		got := removeInt(tc.s, tc.v)
		if len(got) != tc.wantLn {
			t.Errorf("removeInt(%v, %d) len = %d, want %d", tc.s, tc.v, len(got), tc.wantLn)
			continue
		}
		for i := range tc.want {
			if i < len(got) && got[i] != tc.want[i] {
				t.Errorf("removeInt(%v, %d)[%d] = %d, want %d", tc.s, tc.v, i, got[i], tc.want[i])
			}
		}
	}
}

// ---------- joinIntIDs 工具函数 ----------

func TestJoinIntIDs(t *testing.T) {
	if got := joinIntIDs([]int{1, 2, 3}, ", "); got != "1, 2, 3" {
		t.Errorf("joinIntIDs = %q, want %q", got, "1, 2, 3")
	}
	if got := joinIntIDs([]int{}, ", "); got != "" {
		t.Errorf("empty joinIntIDs = %q, want empty", got)
	}
	if got := joinIntIDs([]int{42}, ", "); got != "42" {
		t.Errorf("single joinIntIDs = %q, want 42", got)
	}
	// 中文分隔符
	if got := joinIntIDs([]int{1, 2}, "、"); got != "1、2" {
		t.Errorf("zh separator joinIntIDs = %q, want %q", got, "1、2")
	}
}
