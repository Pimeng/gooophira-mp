package server

import (
	"fmt"
	"strconv"

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
	h.broadcastToMonitors(room, protocol.SrvTouches{Player: int32FromInt(userID), Frames: frames})
}

func (h *Hub) forwardJudges(room *Room, userID int, judges []protocol.JudgeEvent) {
	if h.Monitor != nil {
		h.Monitor.BufferJudges(room, room.MonitorIDs(), userID, judges)
		return
	}
	h.broadcastToMonitors(room, protocol.SrvJudges{Player: int32FromInt(userID), Judges: judges})
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
			room.logRoomInfo(h.MakeRoomLifecycle(room), "log-user-chat", map[string]string{"user": user.Name})
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
		// DEBUG 帧日志：先短路判断等级，避免热路径上无谓的格式化与分配。
		if lg := h.State.Logger; lg != nil && lg.DebugEnabled() {
			lg.Debug(l10n.TL(h.State.ServerLang, "log-user-touches", map[string]string{
				"user": user.Name, "room": string(room.ID), "count": strconv.Itoa(len(c.Frames)),
			}))
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
		// DEBUG 帧日志：先短路判断等级，避免热路径上无谓的格式化与分配。
		if lg := h.State.Logger; lg != nil && lg.DebugEnabled() {
			lg.Debug(l10n.TL(h.State.ServerLang, "log-user-judges", map[string]string{
				"user": user.Name, "room": string(room.ID), "count": strconv.Itoa(len(c.Judges)),
			}))
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
			lc := h.MakeRoomLifecycle(room)
			// 对齐原版：离开房间输出 MARK 级日志（在 OnUserLeave 前取观战后缀）。
			room.logRoomMark(lc, "log-room-left", map[string]string{
				"user": user.Name, "suffix": h.monitorSuffix(user.Monitor),
			})
			shouldDrop := room.OnUserLeave(lc, user)
			if shouldDrop {
				room.logRoomInfo(lc, "log-room-recycled", nil)
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
			room.logRoomMark(h.MakeRoomLifecycle(room), "log-room-lock", map[string]string{
				"user": user.Name, "lock": strconv.FormatBool(c.Lock),
			})
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
			room.logRoomMark(h.MakeRoomLifecycle(room), "log-room-cycle", map[string]string{
				"user": user.Name, "cycle": strconv.FormatBool(c.Cycle),
			})
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
			lc := h.MakeRoomLifecycle(room)
			// 对齐原版：选谱时输出 MARK 级控制台日志。
			room.logRoomMark(lc, "log-room-select-chart", map[string]string{
				"user":   user.Name,
				"userId": fmt.Sprintf("%d", user.ID),
				"chart":  chart.Name,
			})
			h.BroadcastRoomMessage(room, protocol.MsgSelectChart{User: int32FromInt(user.ID), Name: chart.Name, ID: int32FromInt(chart.ID)})
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
			lc := h.MakeRoomLifecycle(room)
			room.logRoomMark(lc, "log-room-request-start", map[string]string{"user": user.Name})
			h.BroadcastRoomMessage(room, protocol.MsgGameStart{User: int32FromInt(user.ID)})
			room.State = StateWaitForReady{Started: map[int]struct{}{user.ID: {}}}
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
			room.logRoomInfo(h.MakeRoomLifecycle(room), "log-room-ready", map[string]string{"user": user.Name})
			h.BroadcastRoomMessage(room, protocol.MsgReady{User: int32FromInt(user.ID)})
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
				room.logRoomMark(lc, "log-room-cancel-game", map[string]string{"user": user.Name})
				h.BroadcastRoomMessage(room, protocol.MsgCancelGame{User: int32FromInt(user.ID)})
				room.State = StateSelectChart{}
				room.OnStateChange(lc)
				room.NotifyWebSocket(lc)
			} else {
				room.logRoomInfo(lc, "log-room-cancel-ready", map[string]string{"user": user.Name})
				h.BroadcastRoomMessage(room, protocol.MsgCancelReady{User: int32FromInt(user.ID)})
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
			room.logRoomMark(h.MakeRoomLifecycle(room), "log-room-played", map[string]string{
				"user": user.Name, "score": strconv.Itoa(record.Score), "acc": fmt.Sprintf("%v", record.Accuracy),
			})
			h.BroadcastRoomMessage(room, protocol.MsgPlayed{
				User: int32FromInt(user.ID), Score: int32FromInt(record.Score),
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
			room.logRoomMark(h.MakeRoomLifecycle(room), "log-room-abort", map[string]string{"user": user.Name})
			h.BroadcastRoomMessage(room, protocol.MsgAbort{User: int32FromInt(user.ID)})
			room.NotifyWebSocket(h.MakeRoomLifecycle(room))
			h.CheckRoomAllReady(room)
			return nil
		})}, true

	default:
		return nil, false
	}
}
