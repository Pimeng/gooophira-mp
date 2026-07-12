package server

import "github.com/Pimeng/gooophira-mp/internal/protocol"

func (r *Room) EmitEvent(state *ServerState, ev Event) {
	if state == nil {
		return
	}
	if ev.RoomID == "" {
		ev.RoomID = r.ID.String()
	}
	if ev.UserCount == 0 {
		ev.UserCount = r.UserCount()
	}
	state.EmitEvent(ev)
}

func (r *Room) EmitUserEvent(state *ServerState, typ EventType, user *User) {
	if user == nil {
		return
	}
	r.EmitEvent(state, Event{Type: typ, UserID: user.ID, UserName: user.Name})
}

func (r *Room) NotifyState(lc *RoomLifecycle) {
	r.OnStateChange(lc)
	r.NotifyWebSocket(lc)
}

func (r *Room) LogAndBroadcast(lc *RoomLifecycle, key string, args map[string]string, msg protocol.Message) {
	if key != "" {
		r.logRoomInfo(lc, key, args)
	}
	r.Send(lc, msg)
}

func (r *Room) MarkAndBroadcast(lc *RoomLifecycle, key string, args map[string]string, msg protocol.Message) {
	if key != "" {
		r.logRoomMark(lc, key, args)
	}
	r.Send(lc, msg)
}

func (r *Room) LogBroadcastAndNotify(lc *RoomLifecycle, key string, args map[string]string, msg protocol.Message) {
	r.LogAndBroadcast(lc, key, args, msg)
	r.NotifyWebSocket(lc)
}

func (r *Room) MarkBroadcastAndNotify(lc *RoomLifecycle, key string, args map[string]string, msg protocol.Message) {
	r.MarkAndBroadcast(lc, key, args, msg)
	r.NotifyWebSocket(lc)
}
