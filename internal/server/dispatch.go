package server

import (
	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

// 聊天内容最大长度（二次防护；协议层已限制 CmdChat ≤200 字节）。
const maxChatLength = 500

func (h *Hub) localize(user *User, key string) string {
	return l10n.TL(user.Lang, key, nil)
}

// unitResult 运行 fn，成功→Ok(Unit)，失败→按用户语言本地化错误 key 的 Err。
func (h *Hub) unitResult(user *User, fn func() error) protocol.StringResult[protocol.Unit] {
	if err := fn(); err != nil {
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

// playingStateFor 返回房间的 Playing 状态及该玩家是否仍可提交（未中止、未交成绩）。
func playingStateFor(room *Room, userID int) (StatePlaying, bool) {
	st, ok := room.State.(StatePlaying)
	if !ok {
		return StatePlaying{}, false
	}
	if _, aborted := st.Aborted[userID]; aborted {
		return st, false
	}
	if _, done := st.Results[userID]; done {
		return st, false
	}
	return st, true
}

func (h *Hub) forwardTouches(room *Room, userID int, frames []protocol.TouchFrame) {
	if h.Monitor != nil {
		h.Monitor.BufferTouches(room, room.MonitorIDs(), userID, frames)
		return
	}
	h.broadcastToMonitors(room, protocol.SrvTouches{Player: int32(userID), Frames: frames})
}

func (h *Hub) forwardJudges(room *Room, userID int, judges []protocol.JudgeEvent) {
	if h.Monitor != nil {
		h.Monitor.BufferJudges(room, room.MonitorIDs(), userID, judges)
		return
	}
	h.broadcastToMonitors(room, protocol.SrvJudges{Player: int32(userID), Judges: judges})
}

func (h *Hub) shouldRecord(room *Room) bool {
	return h.State.ReplayEnabled && room.ReplayEligible && h.State.ReplayRecorder != nil
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
		return protocol.SrvChat{Result: h.unitResult(user, func() error {
			room, err := h.RequireRoom(user)
			if err != nil {
				return err
			}
			content := c.Message
			if !h.State.Config.EffectiveChatEnabled() {
				content = h.localize(user, "chat-disabled-by-server")
			}
			content = truncateRunes(content, maxChatLength)
			room.SendAs(h.MakeRoomLifecycle(room), user, content)
			return nil
		})}, true

	case protocol.CmdTouches:
		room := user.Room
		if room == nil {
			return nil, false
		}
		st, can := playingStateFor(room, user.ID)
		if !can {
			return nil, false
		}
		_ = st
		if len(c.Frames) > 0 {
			user.GameTime = float64(c.Frames[len(c.Frames)-1].Time)
		}
		if room.MonitorCount() > 0 {
			h.forwardTouches(room, user.ID, c.Frames)
		}
		if h.shouldRecord(room) {
			h.State.ReplayRecorder.AppendTouches(room.ID, user.ID, c.Frames)
		}
		return nil, false

	case protocol.CmdJudges:
		room := user.Room
		if room == nil {
			return nil, false
		}
		if _, can := playingStateFor(room, user.ID); !can {
			return nil, false
		}
		if room.MonitorCount() > 0 {
			h.forwardJudges(room, user.ID, c.Judges)
		}
		if h.shouldRecord(room) {
			h.State.ReplayRecorder.AppendJudges(room.ID, user.ID, c.Judges)
		}
		return nil, false

	case protocol.CmdCreateRoom:
		return protocol.SrvCreateRoom{Result: h.unitResult(user, func() error {
			return h.ProcessCreateRoom(user, c.ID)
		})}, true

	case protocol.CmdJoinRoom:
		return protocol.SrvJoinRoom{Result: errToStr(h, user, func() (protocol.JoinRoomResponse, error) {
			return h.ProcessJoinRoom(user, c.ID, c.Monitor)
		})}, true

	case protocol.CmdLeaveRoom:
		return protocol.SrvLeaveRoom{Result: h.unitResult(user, func() error {
			room, err := h.RequireRoom(user)
			if err != nil {
				return err
			}
			shouldDrop := room.OnUserLeave(h.MakeRoomLifecycle(room), user)
			if shouldDrop {
				delete(h.State.Rooms, room.ID)
			} else {
				room.RefreshLive(h.State.ReplayEnabled)
			}
			return nil
		})}, true

	case protocol.CmdLockRoom:
		return protocol.SrvLockRoom{Result: h.unitResult(user, func() error {
			room, err := h.RequireRoom(user)
			if err != nil {
				return err
			}
			if err := room.CheckHost(user); err != nil {
				return err
			}
			room.Locked = c.Lock
			h.BroadcastRoomMessage(room, protocol.MsgLockRoom{Lock: c.Lock})
			return nil
		})}, true

	case protocol.CmdCycleRoom:
		return protocol.SrvCycleRoom{Result: h.unitResult(user, func() error {
			room, err := h.RequireRoom(user)
			if err != nil {
				return err
			}
			if err := room.CheckHost(user); err != nil {
				return err
			}
			room.Cycle = c.Cycle
			h.BroadcastRoomMessage(room, protocol.MsgCycleRoom{Cycle: c.Cycle})
			return nil
		})}, true

	case protocol.CmdSelectChart:
		return protocol.SrvSelectChart{Result: h.unitResult(user, func() error {
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
			h.BroadcastRoomMessage(room, protocol.MsgSelectChart{User: int32(user.ID), Name: chart.Name, ID: int32(chart.ID)})
			lc := h.MakeRoomLifecycle(room)
			room.OnStateChange(lc)
			room.NotifyWebSocket(lc)
			return nil
		})}, true

	case protocol.CmdRequestStart:
		return protocol.SrvRequestStart{Result: h.unitResult(user, func() error {
			room, err := h.RequireRoom(user)
			if err != nil {
				return err
			}
			if err := room.ValidateStart(user); err != nil {
				return err
			}
			room.ResetGameTime(func(id int) *User { return h.State.Users[id] })
			h.BroadcastRoomMessage(room, protocol.MsgGameStart{User: int32(user.ID)})
			room.State = StateWaitForReady{Started: map[int]struct{}{user.ID: {}}}
			lc := h.MakeRoomLifecycle(room)
			room.OnStateChange(lc)
			room.NotifyWebSocket(lc)
			h.CheckRoomAllReady(room)
			return nil
		})}, true

	case protocol.CmdReady:
		return protocol.SrvReady{Result: h.unitResult(user, func() error {
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
			h.BroadcastRoomMessage(room, protocol.MsgReady{User: int32(user.ID)})
			room.NotifyWebSocket(h.MakeRoomLifecycle(room))
			h.CheckRoomAllReady(room)
			return nil
		})}, true

	case protocol.CmdCancelReady:
		return protocol.SrvCancelReady{Result: h.unitResult(user, func() error {
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
				h.BroadcastRoomMessage(room, protocol.MsgCancelGame{User: int32(user.ID)})
				room.State = StateSelectChart{}
				room.OnStateChange(lc)
				room.NotifyWebSocket(lc)
			} else {
				h.BroadcastRoomMessage(room, protocol.MsgCancelReady{User: int32(user.ID)})
				room.NotifyWebSocket(lc)
			}
			return nil
		})}, true

	case protocol.CmdPlayed:
		return protocol.SrvPlayed{Result: h.unitResult(user, func() error {
			room, err := h.RequireRoom(user)
			if err != nil {
				return err
			}
			record, err := h.FetchRecord(user, int(c.ID))
			if err != nil {
				return err
			}
			if record.Player != user.ID {
				return errRecordInvalid
			}
			// 反作弊：成绩须对应本房间当前谱面；record.Chart 缺失（nil）时跳过（fail-open）。
			if room.Chart != nil && record.Chart != nil && *record.Chart != room.Chart.ID {
				return errRecordChartMismatch
			}
			h.BroadcastRoomMessage(room, protocol.MsgPlayed{
				User: int32(user.ID), Score: int32(record.Score),
				Accuracy: float32(record.Accuracy), FullCombo: record.FullCombo,
			})
			st, ok := room.State.(StatePlaying)
			if !ok {
				return nil
			}
			if _, aborted := st.Aborted[user.ID]; aborted {
				return errGameAborted
			}
			if _, done := st.Results[user.ID]; done {
				return errRecordUploaded
			}
			st.Results[user.ID] = record
			if h.shouldRecord(room) {
				h.State.ReplayRecorder.SetRecordID(room.ID, user.ID, record.ID)
			}
			room.NotifyWebSocket(h.MakeRoomLifecycle(room))
			h.CheckRoomAllReady(room)
			return nil
		})}, true

	case protocol.CmdAbort:
		return protocol.SrvAbort{Result: h.unitResult(user, func() error {
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
			h.BroadcastRoomMessage(room, protocol.MsgAbort{User: int32(user.ID)})
			room.NotifyWebSocket(h.MakeRoomLifecycle(room))
			h.CheckRoomAllReady(room)
			return nil
		})}, true

	default:
		return nil, false
	}
}
