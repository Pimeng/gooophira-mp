// dispatch_play.go 把「对局态（Playing）热路径」命令处理从 dispatch.go 拆出：
// 触控帧转发、判定转发、成绩结算（含成绩聊天模板渲染与对象池复用）以及 handlePlayed
// 的异步解散治理。这些函数共享 fillChatRecordMap / stringMapPool 等热路径辅助，集中放置
// 便于排查与未来优化（room-cycle 场景下它们占总分配约 28%，是性能敏感区）。
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
	// 成绩写入后构建排行快照并发射 ScoreSubmitted 事件，供飞书流式更新卡片。
	// EmitEvent 非阻塞（channel 入队），在持 room.Mu 时调用安全。
	rank := BuildScoreRank(room, st)
	scoreEv := Event{Type: EventScoreSubmitted, PlayerScoreRank: rank}
	if room.Chart != nil {
		scoreEv.ChartID, scoreEv.ChartName = room.Chart.ID, room.Chart.Name
	}
	room.EmitEvent(h.State, scoreEv)
	if h.shouldRecord(room) {
		h.State.ReplayRecorder.SetRecordID(room.ID, user.ID, record.ID)
	}
	room.NotifyWebSocket(lc)
	if room.CheckAllReady(lc) {
		// CmdPlayed 是仅房间命令（持 room.Mu），不能同步调用 DisbandRoom：
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

func (h *Hub) handlePlayedResult(user *User, c protocol.CmdPlayed) protocol.StringResult[protocol.Unit] {
	return h.unitResultFromError(user, h.handlePlayed(user, c))
}
