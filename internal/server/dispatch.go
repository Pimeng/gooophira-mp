package server

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/Pimeng/gooophira-mp/internal/config"
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
		h.Monitor.BufferTouches(room, userID, frames)
		return
	}
	h.broadcastToMonitors(room, protocol.SrvTouches{Player: int32FromInt(userID), Frames: frames})
}

func (h *Hub) forwardJudges(room *Room, userID int, judges []protocol.JudgeEvent) {
	if h.Monitor != nil {
		h.Monitor.BufferJudges(room, userID, judges)
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
		if _, can := playingStateFor(room, user.ID); !can {
			return nil, false
		}
		if len(c.Frames) > 0 {
			user.SetGameTime(float64(c.Frames[len(c.Frames)-1].Time))
		}
		// DEBUG 帧日志：先短路判断等级，避免热路径上无谓的格式化与分配。
		if lg := h.State.Logger; lg != nil && lg.DebugEnabled() {
			lg.Debug(fmt.Sprintf("“%s” 在房间 “%s” 上报触控帧 %s 条",
				user.Name, string(room.ID), strconv.Itoa(len(c.Frames))))
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
			lg.Debug(fmt.Sprintf("“%s” 在房间 “%s” 上报判定事件 %s 条",
				user.Name, string(room.ID), strconv.Itoa(len(c.Judges))))
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
			room.Mu.Lock()
			room.logRoomMark(lc, "log-room-left", map[string]string{
				"user": user.Name, "suffix": h.monitorSuffix(user.Monitor),
			})
			shouldDrop, disband := room.OnUserLeave(lc, user)
			if !shouldDrop {
				room.RefreshLive(h.State.ReplayEnabled)
			}
			// 删除房间须在 state.Mu 保护下完成（h.State.Rooms 受其约束）。
			// CmdLeaveRoom 走非 isRoomOnlyCmd 路径，调用方持 state.Mu，故可安全 delete。
			if shouldDrop {
				room.logRoomInfo(lc, "log-room-recycled", nil)
				delete(h.State.Rooms, room.ID)
			}
			room.Mu.Unlock()
			// 比赛 AutoDisband：room.Mu 释放后再调 DisbandRoom（避免重入自死锁）。
			// 调用方持 state.Mu，DisbandRoom 内部 room.Mu.Lock() 安全。
			if disband {
				h.DisbandRoom(room)
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
			// 系统身份提示聊天走 ProtocolHack 延迟调度：让 GameStart 广播先抵达客户端，
			// 提示紧随其后到达，避免在状态切换动画途中弹出系统聊天。
			// 单人房会立即 startPlaying，无需提示「一分钟内准备」，跳过。
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
			room.OnStateChange(lc)
			room.NotifyWebSocket(lc)
			// 先推进状态机：单人房或全员就绪会立即 startPlaying（内部 cancelReadyCountdown），
			// 此时无需启动倒计时；仅在仍处于 WaitForReady 时才启动。
			if room.CheckAllReady(lc) {
				h.DisbandRoom(room)
			} else if _, stillWaiting := room.State.(StateWaitForReady); stillWaiting {
				h.startReadyCountdown(room)
			}
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
			lc := h.MakeRoomLifecycle(room)
			room.logRoomInfo(lc, "log-room-ready", map[string]string{"user": user.Name})
			h.BroadcastRoomMessage(room, protocol.MsgReady{User: int32FromInt(user.ID)})
			room.NotifyWebSocket(lc)
			if room.CheckAllReady(lc) {
				h.DisbandRoom(room)
			}
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
				room.cancelReadyCountdown()
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
			// 已完成（成绩已交 / 中断 / 迟到加入自动标记）则静默成功，避免重复取数、广播与计入。
			if st, ok := room.State.(StatePlaying); ok {
				if _, done := st.Results[user.ID]; done {
					return nil
				}
				if _, aborted := st.Aborted[user.ID]; aborted {
					return nil
				}
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
			lc := h.MakeRoomLifecycle(room)
			room.logRoomMark(lc, "log-room-played", map[string]string{
				"user": user.Name, "score": strconv.Itoa(record.Score), "acc": fmt.Sprintf("%v", record.Accuracy),
			})
			h.BroadcastRoomMessage(room, protocol.MsgChat{
				User:    h.State.SystemChatUserID(),
				Content: l10n.TL(lc.Lang, "chat-record-send-template", buildChatRecordMap(lc.Lang, user, record)),
			})
			st, ok := room.State.(StatePlaying)
			if !ok {
				return nil
			}
			st.Results[user.ID] = record
			firstResult := len(st.Results) == 1
			if h.shouldRecord(room) {
				h.State.ReplayRecorder.SetRecordID(room.ID, user.ID, record.ID)
			}
			room.NotifyWebSocket(lc)
			if room.CheckAllReady(lc) {
				h.DisbandRoom(room)
			} else if firstResult && room.Contest == nil {
				// 首位结算者出现且对局未结束 → 启动结算超时倒计时（仅普通房）。
				// 到点后将未结算玩家标记为 Aborted 并强制结束本局。
				if _, stillPlaying := room.State.(StatePlaying); stillPlaying {
					h.startPlayDeadline(room)
				}
			}
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
			lc := h.MakeRoomLifecycle(room)
			room.logRoomMark(lc, "log-room-abort", map[string]string{"user": user.Name})
			h.BroadcastRoomMessage(room, protocol.MsgAbort{User: int32FromInt(user.ID)})
			room.NotifyWebSocket(lc)
			if room.CheckAllReady(lc) {
				h.DisbandRoom(room)
			}
			return nil
		})}, true

	default:
		return nil, false
	}
}

// recordChatMods 记录 mod 位掩码到本地化键的映射。位值对齐 Phira 客户端 mod 协议：
// 0=自动游玩, 1=X轴翻转, 2=上隐, 3=下隐, 4=夜店, 5=彩虹, 6=无着色器,
// 7=突然死亡(AP), 8=突然死亡(FC)。新增 mod 时按位追加。
var recordChatMods = []struct {
	bit int
	key string
}{
	{1 << 0, "chat-record-mod-autoplay"},
	{1 << 1, "chat-record-mod-flip-x"},
	{1 << 2, "chat-record-mod-hide-top"},
	{1 << 3, "chat-record-mod-hide-bottom"},
	{1 << 4, "chat-record-mod-club"},
	{1 << 5, "chat-record-mod-rainbow"},
	{1 << 6, "chat-record-mod-no-shader"},
	{1 << 7, "chat-record-mod-sudden-death-ap"},
	{1 << 8, "chat-record-mod-sudden-death-fc"},
}

func buildChatRecordMap(lang *l10n.Language, user *User, record config.RecordData) map[string]string {
	hasStd := record.Std != nil && record.StdScore != nil
	hasMod := record.Mod != 0

	m := map[string]string{
		"user":    user.Name,
		"userid":  strconv.Itoa(user.ID),
		"score":   strconv.Itoa(record.Score),
		"acc":     fmt.Sprintf("%.2f", math.Round(record.Accuracy*100)),
		"hasStd":  strconv.FormatBool(hasStd),
		"fc":      strconv.FormatBool(record.FullCombo),
		"isAp":    strconv.FormatBool(math.Round(record.Accuracy*100) == 100),
		"perfect": strconv.Itoa(record.Perfect),
		"good":    strconv.Itoa(record.Good),
		"bad":     strconv.Itoa(record.Bad),
		"miss":    strconv.Itoa(record.Miss),
		"hasMod":  strconv.FormatBool(hasMod),
	}

	if hasStd {
		m["std"] = fmt.Sprintf("%d", int(math.Round(*record.Std*1000)))
		m["stdScore"] = fmt.Sprintf("%.2f", *record.StdScore)
	}

	if hasMod {
		mods := make([]string, 0, len(recordChatMods))
		for _, def := range recordChatMods {
			if record.Mod&def.bit != 0 {
				mods = append(mods, l10n.TL(lang, def.key, nil))
			}
		}
		m["modList"] = strings.Join(mods, ", ")
	}

	return m
}
