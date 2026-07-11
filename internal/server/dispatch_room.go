package server

import (
	"fmt"
	"strconv"

	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

func (h *Hub) handleLockRoom(user *User, c protocol.CmdLockRoom) error {
	room, err := h.RequireRoom(user)
	if err != nil {
		return err
	}
	if err := room.CheckHost(user); err != nil {
		return err
	}
	room.Locked = c.Lock
	room.MarkAndBroadcast(h.MakeRoomLifecycle(room), "log-room-lock", map[string]string{"user": user.Name, "lock": strconv.FormatBool(c.Lock)}, protocol.MsgLockRoom{Lock: c.Lock})
	return nil
}

func (h *Hub) handleCycleRoom(user *User, c protocol.CmdCycleRoom) error {
	room, err := h.RequireRoom(user)
	if err != nil {
		return err
	}
	if err := room.CheckHost(user); err != nil {
		return err
	}
	room.Cycle = c.Cycle
	room.MarkAndBroadcast(h.MakeRoomLifecycle(room), "log-room-cycle", map[string]string{"user": user.Name, "cycle": strconv.FormatBool(c.Cycle)}, protocol.MsgCycleRoom{Cycle: c.Cycle})
	return nil
}

func (h *Hub) handleSelectChart(user *User, c protocol.CmdSelectChart) error {
	room, err := h.RequireRoom(user)
	if err != nil {
		return err
	}
	if err := room.ValidateSelectChart(user); err != nil {
		return err
	}
	chart, err := h.FetchChart(user, int(c.ID))
	if err != nil {
		return err
	}
	room.Chart = &chart
	lc := h.MakeRoomLifecycle(room)
	room.logRoomMark(lc, "log-room-select-chart", map[string]string{"user": user.Name, "userId": fmt.Sprintf("%d", user.ID), "chart": chart.Name})
	h.BroadcastRoomMessage(room, protocol.MsgSelectChart{User: int32FromInt(user.ID), Name: chart.Name, ID: int32FromInt(chart.ID)})
	room.NotifyState(lc)
	return nil
}

func (h *Hub) handleRequestStart(user *User, _ protocol.CmdRequestStart) error {
	room, err := h.RequireRoom(user)
	if err != nil {
		return err
	}
	if err := room.ValidateStart(user); err != nil {
		return err
	}
	room.ResetGameTime(func(id int) *User { return h.State.Users[id] })
	lc := h.MakeRoomLifecycle(room)
	room.logRoomMark(lc, "log-room-request-start", map[string]string{"user": user.Name})
	h.BroadcastRoomMessage(room, protocol.MsgGameStart{User: int32FromInt(user.ID)})
	hint, hasHint := tlOrSkip(lc.Lang, "chat-game-start-hint", map[string]string{"user": user.Name})
	if hasHint && len(room.users) > 1 {
		sysID, state, roomID := h.State.SystemChatUserID(), h.State, room.ID
		h.NewProtocolHack().schedule(func() {
			state.Mu.Lock()
			if state.Rooms[roomID] != room {
				state.Mu.Unlock()
				return
			}
			room.Mu.Lock()
			h.BroadcastRoomMessage(room, protocol.MsgChat{User: sysID, Content: hint})
			room.Mu.Unlock()
			state.Mu.Unlock()
		})
	}
	room.State = StateWaitForReady{Started: map[int]struct{}{user.ID: {}}}
	room.NotifyState(lc)
	if room.CheckAllReady(lc) {
		h.DisbandRoom(room)
	} else if _, ok := room.State.(StateWaitForReady); ok {
		h.startReadyCountdown(room)
	}
	return nil
}

func (h *Hub) handleReady(user *User) error {
	room, err := h.RequireRoom(user)
	if err != nil {
		return err
	}
	if _, ok := room.State.(StatePlaying); ok {
		return ErrRoomInvalidState
	}
	st, ok := room.State.(StateWaitForReady)
	if !ok {
		return nil
	}
	if _, ok := st.Started[user.ID]; ok {
		return errAlreadyReady
	}
	st.Started[user.ID] = struct{}{}
	lc := h.MakeRoomLifecycle(room)
	room.LogBroadcastAndNotify(lc, "log-room-ready", map[string]string{"user": user.Name}, protocol.MsgReady{User: int32FromInt(user.ID)})
	if room.CheckAllReady(lc) {
		h.DisbandRoom(room)
	}
	return nil
}

func (h *Hub) handleCancelReady(user *User) error {
	room, err := h.RequireRoom(user)
	if err != nil {
		return err
	}
	if _, ok := room.State.(StatePlaying); ok {
		return ErrRoomInvalidState
	}
	st, ok := room.State.(StateWaitForReady)
	if !ok {
		return nil
	}
	if _, ok := st.Started[user.ID]; !ok {
		return errNotReady
	}
	delete(st.Started, user.ID)
	lc := h.MakeRoomLifecycle(room)
	if room.HostID == user.ID {
		room.logRoomMark(lc, "log-room-cancel-game", map[string]string{"user": user.Name})
		h.BroadcastRoomMessage(room, protocol.MsgCancelGame{User: int32FromInt(user.ID)})
		room.cancelReadyCountdown()
		room.State = StateSelectChart{}
		room.NotifyState(lc)
		return nil
	}
	room.LogBroadcastAndNotify(lc, "log-room-cancel-ready", map[string]string{"user": user.Name}, protocol.MsgCancelReady{User: int32FromInt(user.ID)})
	return nil
}

func (h *Hub) handleAbort(user *User) error {
	room, err := h.RequireRoom(user)
	if err != nil {
		return err
	}
	st, ok := room.State.(StatePlaying)
	if !ok {
		return nil
	}
	if _, ok := st.Results[user.ID]; ok {
		return errRecordUploaded
	}
	if _, ok := st.Aborted[user.ID]; ok {
		return errGameAborted
	}
	st.Aborted[user.ID] = struct{}{}
	lc := h.MakeRoomLifecycle(room)
	room.MarkBroadcastAndNotify(lc, "log-room-abort", map[string]string{"user": user.Name}, protocol.MsgAbort{User: int32FromInt(user.ID)})
	if room.CheckAllReady(lc) {
		h.DisbandRoom(room)
	}
	return nil
}

func (h *Hub) handleChat(user *User, c protocol.CmdChat) error {
	room, err := h.RequireRoom(user)
	if err != nil {
		return err
	}
	lc := h.MakeRoomLifecycle(room)
	room.logRoomInfo(lc, "log-user-chat", map[string]string{"user": user.Name})
	content := c.Message
	if !h.State.Config.EffectiveChatEnabled() {
		content = h.localize(user, "chat-disabled-by-server")
	}
	room.SendAs(lc, user, truncateRunes(content, maxChatLength))
	return nil
}

func (h *Hub) handleLeaveRoom(user *User) error {
	room, err := h.RequireRoom(user)
	if err != nil {
		return err
	}
	lc := h.MakeRoomLifecycle(room)
	room.Mu.Lock()
	room.logRoomMark(lc, "log-room-left", map[string]string{"user": user.Name, "suffix": h.monitorSuffix(user.Monitor)})
	shouldDrop, disband := room.OnUserLeave(lc, user)
	if !shouldDrop {
		room.RefreshLive(h.State.ReplayEnabled)
	}
	if shouldDrop {
		room.logRoomInfo(lc, "log-room-recycled", nil)
		delete(h.State.Rooms, room.ID)
	}
	room.Mu.Unlock()
	if disband {
		h.DisbandRoom(room)
	}
	return nil
}

func (h *Hub) handleCreateRoom(user *User, c protocol.CmdCreateRoom) error {
	return h.ProcessCreateRoom(user, c.ID)
}

func (h *Hub) handleJoinRoomResult(user *User, c protocol.CmdJoinRoom) protocol.StringResult[protocol.JoinRoomResponse] {
	resp, err := h.ProcessJoinRoom(user, c.ID, c.Monitor)
	if err != nil {
		return protocol.Errr[protocol.JoinRoomResponse](h.localize(user, err.Error()))
	}
	return protocol.Ok(resp)
}
