package server

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"

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

// handlePlayed 处理 CmdPlayed 的核心逻辑，返回 error。
// 从 ProcessClientCommand 的内联闭包提取为独立方法，避免每次调用创建捕获 h/user/c 的闭包
// 堆分配（room-cycle 场景下该闭包约 2.9 GB，占总分配 28%）。
func (h *Hub) handlePlayed(user *User, c protocol.CmdPlayed) error {
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
	logArgs := acquireStringMap()
	logArgs["user"] = user.Name
	logArgs["score"] = strconv.Itoa(record.Score)
	logArgs["acc"] = strconv.FormatFloat(record.Accuracy, 'g', -1, 64)
	room.logRoomMark(lc, "log-room-played", logArgs)
	releaseStringMap(logArgs)
	args := acquireStringMap()
	fillChatRecordMap(args, lc.Lang, user, record)
	content := l10n.TL(lc.Lang, "chat-record-send-template", args)
	releaseStringMap(args)
	h.BroadcastRoomMessage(room, protocol.MsgChat{
		User:    h.State.SystemChatUserID(),
		Content: content,
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
		// CmdPlayed 是 room-only 命令（持 room.Mu），不能同步调 DisbandRoom：
		// DisbandRoom 内部 room.Mu.Lock() 会自死锁，且 delete(state.Rooms) 需 state.Mu
		// （持 room.Mu 获取 state.Mu 会 lock ordering inversion）。
		// 异步执行：网络层释放 room.Mu 后，goroutine 按 state.Mu → room.Mu 顺序获取锁。
		// 指针比较防 room ID 复用边缘情况。
		roomPtr := room
		go func() {
			h.State.Mu.Lock()
			if h.State.Rooms[roomPtr.ID] == roomPtr {
				h.DisbandRoom(roomPtr)
			}
			h.State.Mu.Unlock()
		}()
	} else if firstResult && room.Contest == nil {
		// 首位结算者出现且对局未结束 → 启动结算超时倒计时（仅普通房）。
		// 到点后将未结算玩家标记为 Aborted 并强制结束本局。
		if _, stillPlaying := room.State.(StatePlaying); stillPlaying {
			h.startPlayDeadline(room)
		}
	}
	return nil
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

// stringMapPool 复用 map[string]string，避免 CmdPlayed 热路径上的 map 分配。
// 用途：fillChatRecordMap 的翻译参数 map、logRoomMark 的日志参数 map。
var stringMapPool = sync.Pool{
	New: func() any { return make(map[string]string, 16) },
}

func acquireStringMap() map[string]string {
	m := stringMapPool.Get().(map[string]string)
	for k := range m {
		delete(m, k)
	}
	return m
}

func releaseStringMap(m map[string]string) {
	stringMapPool.Put(m)
}

func fillChatRecordMap(m map[string]string, lang *l10n.Language, user *User, record config.RecordData) {
	hasStd := record.Std != nil && record.StdScore != nil
	hasMod := record.Mod != 0

	m["user"] = user.Name
	m["userid"] = strconv.Itoa(user.ID)
	m["score"] = strconv.Itoa(record.Score)
	m["acc"] = strconv.FormatFloat(record.Accuracy*100, 'g', -1, 64)
	m["hasStd"] = strconv.FormatBool(hasStd)
	m["fc"] = strconv.FormatBool(record.FullCombo)
	m["isAp"] = strconv.FormatBool(math.Round(record.Accuracy*100) == 100)
	m["perfect"] = strconv.Itoa(record.Perfect)
	m["good"] = strconv.Itoa(record.Good)
	m["bad"] = strconv.Itoa(record.Bad)
	m["miss"] = strconv.Itoa(record.Miss)
	m["hasMod"] = strconv.FormatBool(hasMod)

	if hasStd {
		m["std"] = strconv.FormatFloat(float64(int64((*record.Std*1000)*1e6))/1e6, 'g', -1, 64)
		m["stdScore"] = strconv.FormatFloat(*record.StdScore, 'g', -1, 64)
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

func (h *Hub) handleTouches(user *User, c protocol.CmdTouches) (protocol.ServerCommand, bool) {
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
}

func (h *Hub) handleJudges(user *User, c protocol.CmdJudges) (protocol.ServerCommand, bool) {
	room := user.Room
	if room == nil {
		return nil, false
	}
	if _, can := playingStateFor(room, user.ID); !can {
		return nil, false
	}
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

func (h *Hub) handlePlayedResult(user *User, c protocol.CmdPlayed) protocol.StringResult[protocol.Unit] {
	return h.unitResultFromError(user, h.handlePlayed(user, c))
}
