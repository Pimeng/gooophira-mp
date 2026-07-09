package server

import (
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

// joinRoomForTest 将 user 注册到 room 的 usersMap（含 host），并刷新参与者快照，
// 使 BroadcastRoomMessage 经 ParticipantsSnapshot 能投递到该用户的 mockSession。
// 复用 roomlogic.go OnUserJoin 的核心副作用，避免完整走 Hub 流程。
func joinRoomForTest(room *Room, user *User) {
	room.Mu.Lock()
	if _, exists := room.usersMap[user.ID]; !exists {
		room.users = append(room.users, user.ID)
	}
	room.usersMap[user.ID] = user
	room.refreshParticipantsSnapshot()
	room.Mu.Unlock()
}

// findMsgInSent 在用户收到的命令中查找指定类型的 Message，返回最后一个匹配与是否找到。
// mockSession.sent 是累积的，取最后一个反映最新广播的状态。
func findMsgInSent[T protocol.Message](sent []protocol.ServerCommand) (T, bool) {
	var zero T
	var found bool
	for _, c := range sent {
		if sm, ok := c.(protocol.SrvMessage); ok {
			if m, ok := sm.Message.(T); ok {
				zero = m
				found = true
			}
		}
	}
	return zero, found
}

func TestSetRoomLocked_BroadcastsAndTogglesField(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	host := h.addUser(1, "host")
	room := NewRoom("r1", 1, 8, false)
	h.state.Rooms[room.ID] = room
	joinRoomForTest(room, host)

	hub.SetRoomLocked(room, true)
	if !room.Locked {
		t.Fatal("room.Locked should be true after lock")
	}
	m, ok := findMsgInSent[protocol.MsgLockRoom](sentTo(host))
	if !ok {
		t.Fatal("host should receive MsgLockRoom broadcast")
	}
	if !m.Lock {
		t.Errorf("MsgLockRoom.Lock = false, want true")
	}

	hub.SetRoomLocked(room, false)
	if room.Locked {
		t.Fatal("room.Locked should be false after unlock")
	}
	m, ok = findMsgInSent[protocol.MsgLockRoom](sentTo(host))
	if !ok || m.Lock {
		t.Errorf("second MsgLockRoom broadcast wrong, got %+v ok=%v", m, ok)
	}
}

func TestSetRoomCycle_BroadcastsAndTogglesField(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	host := h.addUser(1, "host")
	room := NewRoom("r1", 1, 8, false)
	h.state.Rooms[room.ID] = room
	joinRoomForTest(room, host)

	hub.SetRoomCycle(room, true)
	if !room.Cycle {
		t.Fatal("room.Cycle should be true after enable")
	}
	m, ok := findMsgInSent[protocol.MsgCycleRoom](sentTo(host))
	if !ok {
		t.Fatal("host should receive MsgCycleRoom broadcast")
	}
	if !m.Cycle {
		t.Errorf("MsgCycleRoom.Cycle = false, want true")
	}

	hub.SetRoomCycle(room, false)
	if room.Cycle {
		t.Fatal("room.Cycle should be false after disable")
	}
}

func TestTransferHost_SuccessBroadcastsAndNotifiesBoth(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	host := h.addUser(1, "host")
	player := h.addUser(2, "player")
	room := NewRoom("r1", 1, 8, false)
	h.state.Rooms[room.ID] = room
	joinRoomForTest(room, host)
	joinRoomForTest(room, player)

	if err := hub.TransferHost(room, 2); err != nil {
		t.Fatalf("TransferHost failed: %v", err)
	}
	if room.HostID != 2 {
		t.Errorf("room.HostID = %d, want 2", room.HostID)
	}

	hostSent := sentTo(host)
	playerSent := sentTo(player)

	// 旧房主应收到 SrvChangeHost{IsHost:false}（经 TrySend 直接发送）。
	var hostChange protocol.SrvChangeHost
	found := false
	for _, c := range hostSent {
		if ch, ok := c.(protocol.SrvChangeHost); ok {
			hostChange = ch
			found = true
		}
	}
	if !found {
		t.Fatal("old host should receive SrvChangeHost")
	}
	if hostChange.IsHost {
		t.Error("old host SrvChangeHost.IsHost = true, want false")
	}

	// 新房主应收到 SrvChangeHost{IsHost:true}。
	var playerChange protocol.SrvChangeHost
	found = false
	for _, c := range playerSent {
		if ch, ok := c.(protocol.SrvChangeHost); ok {
			playerChange = ch
			found = true
		}
	}
	if !found {
		t.Fatal("new host should receive SrvChangeHost")
	}
	if !playerChange.IsHost {
		t.Error("new host SrvChangeHost.IsHost = false, want true")
	}

	// 两者都应收到 MsgNewHost 广播。
	if _, ok := findMsgInSent[protocol.MsgNewHost](hostSent); !ok {
		t.Error("old host should receive MsgNewHost broadcast")
	}
	mh, ok := findMsgInSent[protocol.MsgNewHost](playerSent)
	if !ok {
		t.Fatal("new host should receive MsgNewHost broadcast")
	}
	if mh.User != 2 {
		t.Errorf("MsgNewHost.User = %d, want 2", mh.User)
	}

	// nextHostID 应被清除，避免与 nexthost 预约冲突。
	if _, set := room.NextHostID(); set {
		t.Error("room.nextHostID should be cleared after admin host transfer")
	}
}

func TestTransferHost_UserNotInRoom(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	host := h.addUser(1, "host")
	room := NewRoom("r1", 1, 8, false)
	h.state.Rooms[room.ID] = room
	joinRoomForTest(room, host)

	err := hub.TransferHost(room, 999)
	if err != ErrUserNotInRoom {
		t.Errorf("TransferHost for absent user = %v, want ErrUserNotInRoom", err)
	}
	if room.HostID != 1 {
		t.Errorf("room.HostID changed to %d on failed transfer", room.HostID)
	}
	if len(sentTo(host)) != 0 {
		t.Error("no broadcast should occur on failed transfer")
	}
}

func TestTransferHost_AlreadyHostNoOp(t *testing.T) {
	h := newHarness()
	hub := NewHub(h.state, &mockPhira{})
	host := h.addUser(1, "host")
	room := NewRoom("r1", 1, 8, false)
	h.state.Rooms[room.ID] = room
	joinRoomForTest(room, host)

	err := hub.TransferHost(room, 1)
	if err != ErrAlreadyHost {
		t.Errorf("TransferHost to current host = %v, want ErrAlreadyHost", err)
	}
	if len(sentTo(host)) != 0 {
		t.Error("no broadcast/notify should occur when target is already host")
	}
}
