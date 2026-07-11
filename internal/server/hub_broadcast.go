package server

import (
	"sort"
	"sync"

	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

func pickNextHost(ids []int, oldHostID int) (int, bool) {
	if len(ids) == 0 {
		return 0, false
	}
	sorted := append([]int(nil), ids...)
	sort.Ints(sorted)
	for _, id := range sorted {
		if id > oldHostID {
			return id, true
		}
	}
	return sorted[0], true
}

func (h *Hub) BroadcastRoom(room *Room, cmd protocol.ServerCommand) {
	users := room.ParticipantsSnapshot()
	if len(users) == 0 {
		return
	}
	frame := encodeServerCommandFrame(cmd)
	if frame == nil {
		return
	}
	for _, u := range users {
		u.TrySendFrameOwned(frame)
	}
}

func (h *Hub) BroadcastRoomExcept(room *Room, cmd protocol.ServerCommand, exclude map[int]struct{}) {
	users := room.ParticipantsSnapshot()
	if len(users) == 0 {
		return
	}
	frame := encodeServerCommandFrame(cmd)
	if frame == nil {
		return
	}
	for _, u := range users {
		if _, skip := exclude[u.ID]; skip {
			continue
		}
		u.TrySendFrameOwned(frame)
	}
}

func (h *Hub) broadcastToMonitors(room *Room, cmd protocol.ServerCommand) {
	users := room.MonitorUsers()
	if len(users) == 0 {
		return
	}
	frame := encodeServerCommandFrame(cmd)
	if frame == nil {
		return
	}
	for _, u := range users {
		u.TrySendFrameOwned(frame)
	}
}

func encodeServerCommandFrame(cmd protocol.ServerCommand) []byte {
	w := serverFrameWriterPool.Get().(*protocol.BinaryWriter)
	defer serverFrameWriterPool.Put(w)
	w.Reset()
	protocol.EncodeServerCommand(w, cmd)
	fb := w.ToFrameBuffer()
	frame := make([]byte, len(fb))
	copy(frame, fb)
	return frame
}

var serverFrameWriterPool = &sync.Pool{
	New: func() any { return protocol.NewFrameWriter(5) },
}

func (h *Hub) BroadcastRoomMessage(room *Room, msg protocol.Message) {
	room.Send(h.MakeRoomLifecycle(room), msg)
}

func (h *Hub) monitorSuffix(monitor bool) string {
	if !monitor {
		return ""
	}
	return l10n.TL(h.State.ServerLang, "label-monitor-suffix", nil)
}

func (h *Hub) MakeRoomLifecycle(room *Room) *RoomLifecycle {
	if lc := room.lifecycle.Load(); lc != nil {
		return lc
	}
	lc := &RoomLifecycle{
		State:               h.State,
		UsersByID:           func(id int) *User { return room.usersMap[id] },
		Broadcast:           func(cmd protocol.ServerCommand) { h.BroadcastRoom(room, cmd) },
		BroadcastExcept:     func(cmd protocol.ServerCommand, exclude map[int]struct{}) { h.BroadcastRoomExcept(room, cmd, exclude) },
		BroadcastToMonitors: func(cmd protocol.ServerCommand) { h.broadcastToMonitors(room, cmd) },
		PickNextHostID:      pickNextHost,
		Lang:                h.State.ServerLang,
		Logger:              h.State.Logger,
		DisbandRoom:         h.DisbandRoom,
		OnEnterPlaying:      h.OnEnterPlaying,
		OnGameEnd:           h.OnGameEnd,
		WSService:           h.State.WSService,
		SystemChatUserID:    h.State.SystemChatUserID,
	}
	room.lifecycle.Store(lc)
	return lc
}

func (r *Room) clientRoomStateForJoin() protocol.RoomState {
	st := r.ClientRoomState()
	if _, isSelect := r.State.(StateSelectChart); !isSelect && r.Chart != nil {
		cid := int32(r.Chart.ID)
		st = protocol.RoomStateSelectChart{ID: &cid}
	}
	return st
}

func (h *Hub) ClientRoomStateForJoin(room *Room) protocol.RoomState {
	return room.clientRoomStateForJoin()
}

func (h *Hub) RequireRoom(user *User) (*Room, error) {
	if user.Room == nil {
		return nil, errNoRoom
	}
	return user.Room, nil
}

func (h *Hub) CheckRoomAllReady(room *Room) bool {
	return room.CheckAllReady(h.MakeRoomLifecycle(room))
}
