package server

import (
	"fmt"
	"strconv"

	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

// 聊天内容最大长度（rune 计）。协议层已将 CmdChat.Message 截到 200 字节，
// 本常量主要兜底服务端拼装的 chat-disabled-by-server 等本地化文案，避免异常长串。
const maxChatLength = 500

func (h *Hub) localize(user *User, key string) string {
	return l10n.TL(user.Lang, key, nil)
}

// tlOrSkip 返回本地化文本；若 key 在 lang 中缺失（TL 返回 key 本身或空串）则 ok=false。
// 用于系统聊天提示这类「缺失即跳过」的场景，统一原本散落的 hint == "" || hint == key 检查。
func tlOrSkip(lang *l10n.Language, key string, args map[string]string) (text string, ok bool) {
	s := l10n.TL(lang, key, args)
	if s == "" || s == key {
		return "", false
	}
	return s, true
}

// unitResult 运行 fn，成功→Ok(Unit)，失败→按用户语言本地化错误 key 的 Err。
func (h *Hub) unitResult(user *User, fn func() error) protocol.StringResult[protocol.Unit] {
	if err := fn(); err != nil {
		return protocol.Errr[protocol.Unit](h.localize(user, err.Error()))
	}
	return protocol.Ok(protocol.Unit{})
}

// unitResultFromError 同 unitResult，但直接接收 error 而非闭包。
// 用于热路径（如 CmdPlayed）：避免闭包捕获 h/user/c 导致的堆分配
// （room-cycle 场景下闭包分配约 2.9 GB，占总分配 28%）。
func (h *Hub) unitResultFromError(user *User, err error) protocol.StringResult[protocol.Unit] {
	if err != nil {
		return protocol.Errr[protocol.Unit](h.localize(user, err.Error()))
	}
	return protocol.Ok(protocol.Unit{})
}

// errToStr 同 unitResult，但携带返回值 T。
func errToStr[T any](h *Hub, user *User, fn func() (T, error)) protocol.StringResult[T] {
	v, err := fn()
	if err != nil {
		return protocol.Errr[T](h.localize(user, err.Error()))
	}
	return protocol.Ok(v)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// ProcessClientCommand 处理一条已认证用户的客户端命令，返回需回复的命令（ok=false 表示无需回复）。
// 对应 TS network/session/commandRouter.processClientCommand。
func (h *Hub) ProcessClientCommand(user *User, cmd protocol.ClientCommand) (protocol.ServerCommand, bool) {
	switch c := cmd.(type) {
	case protocol.CmdPing:
		return nil, false

	case protocol.CmdAuthenticate:
		_ = c
		return protocol.SrvAuthenticate{Result: protocol.Errr[protocol.AuthInfo](h.localize(user, errAuthRepeated.Error()))}, true

	case protocol.CmdChat:
		return protocol.SrvChat{Result: h.unitResultFromError(user, h.handleChat(user, c))}, true

	case protocol.CmdTouches:
		return h.handleTouches(user, c)

	case protocol.CmdJudges:
		return h.handleJudges(user, c)

	case protocol.CmdCreateRoom:
		return protocol.SrvCreateRoom{Result: h.unitResultFromError(user, h.handleCreateRoom(user, c))}, true

	case protocol.CmdJoinRoom:
		return protocol.SrvJoinRoom{Result: h.handleJoinRoomResult(user, c)}, true

	case protocol.CmdLeaveRoom:
		return protocol.SrvLeaveRoom{Result: h.unitResultFromError(user, h.handleLeaveRoom(user))}, true

	case protocol.CmdLockRoom:
		return protocol.SrvLockRoom{Result: h.unitResultFromError(user, h.handleLockRoom(user, c))}, true

	case protocol.CmdCycleRoom:
		return protocol.SrvCycleRoom{Result: h.unitResultFromError(user, h.handleCycleRoom(user, c))}, true

	case protocol.CmdSelectChart:
		return protocol.SrvSelectChart{Result: h.unitResultFromError(user, h.handleSelectChart(user, c))}, true

	case protocol.CmdRequestStart:
		return protocol.SrvRequestStart{Result: h.unitResultFromError(user, h.handleRequestStart(user, c))}, true

	case protocol.CmdReady:
		return protocol.SrvReady{Result: h.unitResultFromError(user, h.handleReady(user))}, true

	case protocol.CmdCancelReady:
		return protocol.SrvCancelReady{Result: h.unitResultFromError(user, h.handleCancelReady(user))}, true

	case protocol.CmdPlayed:
		return protocol.SrvPlayed{Result: h.handlePlayedResult(user, c)}, true

	case protocol.CmdAbort:
		return protocol.SrvAbort{Result: h.unitResultFromError(user, h.handleAbort(user))}, true

	default:
		return nil, false
	}
}

func (h *Hub) handleLockRoom(user *User, c protocol.CmdLockRoom) error {
	room, err := h.RequireRoom(user)
	if err != nil {
		return err
	}
	if err := room.CheckHost(user); err != nil {
		return err
	}
	room.Locked = c.Lock
	room.MarkAndBroadcast(h.MakeRoomLifecycle(room), "log-room-lock", map[string]string{
		"user": user.Name, "lock": strconv.FormatBool(c.Lock),
	}, protocol.MsgLockRoom{Lock: c.Lock})
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
	room.MarkAndBroadcast(h.MakeRoomLifecycle(room), "log-room-cycle", map[string]string{
		"user": user.Name, "cycle": strconv.FormatBool(c.Cycle),
	}, protocol.MsgCycleRoom{Cycle: c.Cycle})
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
	room.logRoomMark(lc, "log-room-select-chart", map[string]string{
		"user":   user.Name,
		"userId": fmt.Sprintf("%d", user.ID),
		"chart":  chart.Name,
	})
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
		sysID := h.State.SystemChatUserID()
		state := h.State
		roomID := room.ID
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
	} else if _, stillWaiting := room.State.(StateWaitForReady); stillWaiting {
		h.startReadyCountdown(room)
	}
	return nil
}

func (h *Hub) handleReady(user *User) error {
	room, err := h.RequireRoom(user)
	if err != nil {
		return err
	}
	if _, playing := room.State.(StatePlaying); playing {
		return ErrRoomInvalidState
	}
	st, ok := room.State.(StateWaitForReady)
	if !ok {
		return nil
	}
	if _, already := st.Started[user.ID]; already {
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
	if _, playing := room.State.(StatePlaying); playing {
		return ErrRoomInvalidState
	}
	st, ok := room.State.(StateWaitForReady)
	if !ok {
		return nil
	}
	if _, ready := st.Started[user.ID]; !ready {
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
	if _, done := st.Results[user.ID]; done {
		return errRecordUploaded
	}
	if _, aborted := st.Aborted[user.ID]; aborted {
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
	content = truncateRunes(content, maxChatLength)
	room.SendAs(lc, user, content)
	return nil
}

func (h *Hub) handleLeaveRoom(user *User) error {
	room, err := h.RequireRoom(user)
	if err != nil {
		return err
	}
	lc := h.MakeRoomLifecycle(room)
	room.Mu.Lock()
	room.logRoomMark(lc, "log-room-left", map[string]string{
		"user": user.Name, "suffix": h.monitorSuffix(user.Monitor),
	})
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