package server

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

// RoomLifecycle 是房间生命周期方法（onUserLeave/checkAllReady/send 等）共享的依赖注入：
// 广播、用户查询、随机选主、本地化、日志与状态钩子。对应 TS 的 RoomLifecycleOptions。
//
// 广播在 Go 中按同步语义处理（回调内部自行决定并发扇出）；网络层的并发发送在 Stage 4
// 的 session 层处理。
type RoomLifecycle struct {
	UsersByID           func(id int) *User
	Broadcast           func(cmd protocol.ServerCommand)
	BroadcastToMonitors func(cmd protocol.ServerCommand)
	PickRandomUserID    func(ids []int) (int, bool)
	Lang                *l10n.Language
	Logger              Logger
	DisbandRoom         func(room *Room)
	OnEnterPlaying      func(room *Room)
	OnGameEnd           func(room *Room)
	WSService           WsBroadcaster
}

func removeInt(s []int, v int) []int {
	if i := slices.Index(s, v); i >= 0 {
		return slices.Delete(s, i, i+1)
	}
	return s
}

// AddUser 把用户加入房间（玩家或观战者）。玩家超过 MaxUsers 时返回 false。
func (r *Room) AddUser(user *User, monitor bool) bool {
	if monitor {
		if !slices.Contains(r.monitors, user.ID) {
			r.monitors = append(r.monitors, user.ID)
		}
		return true
	}
	if len(r.users) >= r.MaxUsers {
		return false
	}
	if !slices.Contains(r.users, user.ID) {
		r.users = append(r.users, user.ID)
	}
	return true
}

// ClientState 组装发给指定用户的完整房间状态视图（含成员 UserInfo）。
func (r *Room) ClientState(user *User, usersByID func(id int) *User) protocol.ClientRoomState {
	infoMap := make(map[int32]protocol.UserInfo, len(r.users)+len(r.monitors))
	for _, id := range r.AllParticipantIDs() {
		if u := usersByID(id); u != nil {
			infoMap[int32(id)] = u.ToInfo()
		}
	}
	isReady := false
	if s, ok := r.State.(StateWaitForReady); ok {
		_, isReady = s.Started[user.ID]
	}
	return protocol.ClientRoomState{
		ID:      r.ID,
		State:   r.ClientRoomState(),
		Live:    r.Live,
		Locked:  r.Locked,
		Cycle:   r.Cycle,
		IsHost:  r.HostID == user.ID,
		IsReady: isReady,
		Users:   infoMap,
	}
}

// OnStateChange 向房间广播状态变更。
func (r *Room) OnStateChange(lc *RoomLifecycle) {
	lc.Broadcast(protocol.SrvChangeState{State: r.ClientRoomState()})
}

// NotifyWebSocket 触发 WebSocket 房间/管理面板增量推送（若启用）。
func (r *Room) NotifyWebSocket(lc *RoomLifecycle) {
	if lc.WSService != nil {
		lc.WSService.BroadcastRoomUpdate(r.ID)
		lc.WSService.BroadcastAdminUpdate()
	}
}

// Send 向房间广播一条消息，并按需记录房间日志。
func (r *Room) Send(lc *RoomLifecycle, msg protocol.Message) {
	if chat, ok := msg.(protocol.MsgChat); ok {
		r.AddLog(chat.Content, time.Now().UnixMilli())
	} else if lc.Lang != nil {
		if logText := r.formatMessageForLog(msg, lc); logText != "" {
			r.AddLog(logText, time.Now().UnixMilli())
		}
	}
	lc.Broadcast(protocol.SrvMessage{Message: msg})
}

// SendAs 以某用户身份向房间广播聊天。
func (r *Room) SendAs(lc *RoomLifecycle, user *User, content string) {
	r.Send(lc, protocol.MsgChat{User: int32(user.ID), Content: content})
}

func (r *Room) nameOf(lc *RoomLifecycle, id int) string {
	if u := lc.UsersByID(id); u != nil {
		return u.Name
	}
	return fmt.Sprintf("%d", id)
}

// formatMessageForLog 把房间消息渲染为本地化日志文本（无需记录则返回 ""）。
func (r *Room) formatMessageForLog(msg protocol.Message, lc *RoomLifecycle) string {
	tl := func(key string, args map[string]string) string { return l10n.TL(lc.Lang, key, args) }
	switch m := msg.(type) {
	case protocol.MsgCreateRoom:
		return tl("log-msg-create-room", map[string]string{"user": r.nameOf(lc, int(m.User))})
	case protocol.MsgJoinRoom:
		return tl("log-msg-join-room", map[string]string{"name": m.Name})
	case protocol.MsgLeaveRoom:
		return tl("log-msg-leave-room", map[string]string{"name": m.Name})
	case protocol.MsgNewHost:
		return tl("log-msg-new-host", map[string]string{"user": r.nameOf(lc, int(m.User))})
	case protocol.MsgSelectChart:
		return tl("log-msg-select-chart", map[string]string{"user": r.nameOf(lc, int(m.User)), "name": m.Name, "id": fmt.Sprintf("%d", m.ID)})
	case protocol.MsgGameStart:
		return tl("log-msg-game-start", map[string]string{"user": r.nameOf(lc, int(m.User))})
	case protocol.MsgReady:
		return tl("log-msg-ready", map[string]string{"user": r.nameOf(lc, int(m.User))})
	case protocol.MsgCancelReady:
		return tl("log-msg-cancel-ready", map[string]string{"user": r.nameOf(lc, int(m.User))})
	case protocol.MsgCancelGame:
		return tl("log-msg-cancel-game", map[string]string{"user": r.nameOf(lc, int(m.User))})
	case protocol.MsgStartPlaying:
		return tl("log-msg-start-playing", nil)
	case protocol.MsgPlayed:
		return tl("log-msg-played", map[string]string{
			"user":  r.nameOf(lc, int(m.User)),
			"score": fmt.Sprintf("%d", m.Score),
			"acc":   fmt.Sprintf("%.2f", m.Accuracy*100),
			"fc":    strconv.FormatBool(m.FullCombo),
		})
	case protocol.MsgGameEnd:
		return tl("log-msg-game-end", nil)
	case protocol.MsgAbort:
		return tl("log-msg-abort", map[string]string{"user": r.nameOf(lc, int(m.User))})
	case protocol.MsgLockRoom:
		return tl("log-msg-lock-room", map[string]string{"lock": strconv.FormatBool(m.Lock)})
	case protocol.MsgCycleRoom:
		return tl("log-msg-cycle-room", map[string]string{"cycle": strconv.FormatBool(m.Cycle)})
	default:
		return ""
	}
}

// ResetGameTime 把房内所有玩家的 gameTime 重置为 -Inf（新一局开始时）。
func (r *Room) ResetGameTime(usersByID func(id int) *User) {
	for _, id := range r.users {
		if u := usersByID(id); u != nil {
			u.GameTime = math.Inf(-1)
		}
	}
}

// OnUserLeave 处理用户离开房间：广播、移除成员、必要时转移房主，并检查就绪/结算。
// 返回房间是否应被解散（已无任何成员）。
func (r *Room) OnUserLeave(lc *RoomLifecycle, user *User) bool {
	r.Send(lc, protocol.MsgLeaveRoom{User: int32(user.ID), Name: user.Name})
	user.Room = nil

	if user.Monitor {
		r.monitors = removeInt(r.monitors, user.ID)
	} else {
		r.users = removeInt(r.users, user.ID)
	}

	if r.HostID == user.ID {
		if len(r.users) == 0 {
			return true
		}
		newHost, ok := lc.PickRandomUserID(r.UserIDs())
		if !ok {
			return true
		}
		r.HostID = newHost
		r.logRoomInfo(lc, "log-room-host-changed-offline", map[string]string{
			"old": fmt.Sprintf("%d", user.ID), "next": fmt.Sprintf("%d", newHost),
		})
		r.Send(lc, protocol.MsgNewHost{User: int32(newHost)})
		if nh := lc.UsersByID(newHost); nh != nil {
			nh.TrySend(protocol.SrvChangeHost{IsHost: true})
		}
	}

	r.NotifyWebSocket(lc)
	r.CheckAllReady(lc)
	return r.IsEmpty()
}

func (r *Room) logRoomInfo(lc *RoomLifecycle, key string, args map[string]string) {
	if lc.Logger == nil {
		return
	}
	// 对齐原版 logRoom：把房间 id 作为 `room` 参数注入（FTL 消息以 { $room } 引用）。
	if args == nil {
		args = map[string]string{}
	}
	args["room"] = string(r.ID)
	lc.Logger.Info(l10n.TL(lc.Lang, key, args))
}

// CheckAllReady 推进房间状态机：
//   - WaitForReady：全员就绪（且非比赛手动开始）则进入 Playing；
//   - Playing：全员完成（成绩或中止）则结算并回到 SelectChart（含比赛自动解散 / cycle 轮换）。
func (r *Room) CheckAllReady(lc *RoomLifecycle) {
	switch st := r.State.(type) {
	case StateWaitForReady:
		r.checkWaitForReady(lc, st)
	case StatePlaying:
		r.checkPlaying(lc, st)
	}
}

func (r *Room) checkWaitForReady(lc *RoomLifecycle, st StateWaitForReady) {
	for _, id := range r.AllParticipantIDs() {
		if _, ok := st.Started[id]; !ok {
			return // 还有人未就绪
		}
	}
	if r.Contest != nil && r.Contest.ManualStart {
		return // 比赛房：全员就绪后仍等待管理员手动开赛
	}
	r.startPlaying(lc)
}

// startPlaying 把房间切到 Playing 并广播开赛（触发录制钩子、重置 gameTime）。
// 供「全员就绪自动开赛」与「管理员/比赛强制开赛」共用。
func (r *Room) startPlaying(lc *RoomLifecycle) {
	if lc.OnEnterPlaying != nil {
		lc.OnEnterPlaying(r)
	}
	r.logRoomInfo(lc, "log-room-game-start", nil)
	r.Send(lc, protocol.MsgStartPlaying{})
	r.ResetGameTime(lc.UsersByID)
	r.State = StatePlaying{
		Results: make(map[int]config.RecordData),
		Aborted: make(map[int]struct{}),
	}
	r.OnStateChange(lc)
	r.NotifyWebSocket(lc)
}

func (r *Room) checkPlaying(lc *RoomLifecycle, st StatePlaying) {
	finished := true
	for _, id := range r.users {
		_, hasResult := st.Results[id]
		_, hasAbort := st.Aborted[id]
		if !hasResult && !hasAbort {
			finished = false
			break
		}
	}
	if !finished {
		r.notifyDanglingReconnect(lc, &st)
		return
	}

	if len(st.Results) > 0 {
		r.broadcastGameSummary(lc, st)
	}
	r.logRoomInfo(lc, "log-room-game-end", map[string]string{
		"uploaded": fmt.Sprintf("%d", len(st.Results)),
		"aborted":  fmt.Sprintf("%d", len(st.Aborted)),
	})
	r.Send(lc, protocol.MsgGameEnd{})
	if lc.OnGameEnd != nil {
		lc.OnGameEnd(r)
	}
	r.State = StateSelectChart{}

	if r.Contest != nil && r.Contest.AutoDisband && lc.DisbandRoom != nil {
		lc.DisbandRoom(r)
		return
	}

	if r.IsCycle() && len(r.users) > 1 {
		r.rotateCycleHost(lc)
	}

	r.OnStateChange(lc)
	r.NotifyWebSocket(lc)
}

// notifyDanglingReconnect 在「其他玩家都已完成、仅剩断线挂起玩家未完成」时，向房间播报
// 一次「正在等待重连 + 剩余倒计时」提示（每名挂起玩家仅播报一次）。
func (r *Room) notifyDanglingReconnect(lc *RoomLifecycle, st *StatePlaying) {
	var unfinished, dangling []int
	for _, id := range r.users {
		_, hasResult := st.Results[id]
		_, hasAbort := st.Aborted[id]
		if hasResult || hasAbort {
			continue
		}
		unfinished = append(unfinished, id)
		if u := lc.UsersByID(id); u == nil || u.Session == nil {
			dangling = append(dangling, id)
		}
	}
	if len(unfinished) == 0 || len(dangling) != len(unfinished) {
		return
	}
	if st.ReconnectNotified == nil {
		st.ReconnectNotified = make(map[int]struct{})
		// 回写到房间状态（st 是值拷贝）。
		r.State = *st
	}
	for _, id := range dangling {
		if _, done := st.ReconnectNotified[id]; done {
			continue
		}
		u := lc.UsersByID(id)
		if u == nil {
			continue
		}
		st.ReconnectNotified[id] = struct{}{}
		seconds := 0
		if u.DangleDeadline != nil {
			remain := (*u.DangleDeadline - time.Now().UnixMilli())
			seconds = max(1, int(math.Ceil(float64(remain)/1000)))
		}
		r.Send(lc, protocol.MsgChat{User: 0, Content: l10n.TL(lc.Lang, "chat-waiting-reconnect",
			map[string]string{"user": u.Name, "seconds": fmt.Sprintf("%d", seconds)})})
	}
}

// broadcastGameSummary 计算并播报本局最佳成绩（分数 / 准度 / std）摘要。
// 平局时取 id 升序的第一名（确定性；TS 取 Played 提交顺序，此处为显示细节差异）。
func (r *Room) broadcastGameSummary(lc *RoomLifecycle, st StatePlaying) {
	ids := make([]int, 0, len(st.Results))
	for id := range st.Results {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	bestScore, bestAcc := math.Inf(-1), math.Inf(-1)
	bestStd := math.Inf(1)
	var bestScoreID, bestAccID, bestStdID int
	for _, id := range ids {
		res := st.Results[id]
		if float64(res.Score) > bestScore {
			bestScore = float64(res.Score)
			bestScoreID = id
		}
		if res.Accuracy > bestAcc {
			bestAcc = res.Accuracy
			bestAccID = id
		}
		if res.Std < bestStd {
			bestStd = res.Std
			bestStdID = id
		}
	}

	tl := func(key string, args map[string]string) string { return l10n.TL(lc.Lang, key, args) }
	scoreText := tl("chat-game-summary-score", map[string]string{
		"name": r.nameOf(lc, bestScoreID), "id": fmt.Sprintf("%d", bestScoreID), "score": fmt.Sprintf("%d", int(bestScore)),
	})
	accText := tl("chat-game-summary-acc", map[string]string{
		"name": r.nameOf(lc, bestAccID), "id": fmt.Sprintf("%d", bestAccID), "acc": fmt.Sprintf("%.2f%%", bestAcc*100),
	})
	stdText := tl("chat-game-summary-std", map[string]string{
		"name": r.nameOf(lc, bestStdID), "id": fmt.Sprintf("%d", bestStdID), "std": fmt.Sprintf("%d", int(math.Round(bestStd*1000))),
	})
	summary := tl("chat-game-summary", map[string]string{"scoreText": scoreText, "accText": accText, "stdText": stdText})
	r.Send(lc, protocol.MsgChat{User: 0, Content: summary})
}

// rotateCycleHost 在 cycle 模式下把房主轮换到加入顺序的下一位。
func (r *Room) rotateCycleHost(lc *RoomLifecycle) {
	idx := max(0, slices.Index(r.users, r.HostID))
	newHost := r.users[(idx+1)%len(r.users)]
	oldHost := r.HostID
	r.HostID = newHost
	r.logRoomInfo(lc, "log-room-host-changed-cycle", map[string]string{
		"old": fmt.Sprintf("%d", oldHost), "next": fmt.Sprintf("%d", newHost),
	})
	r.Send(lc, protocol.MsgNewHost{User: int32(newHost)})
	if oh := lc.UsersByID(oldHost); oh != nil {
		oh.TrySend(protocol.SrvChangeHost{IsHost: false})
	}
	if nh := lc.UsersByID(newHost); nh != nil {
		nh.TrySend(protocol.SrvChangeHost{IsHost: true})
	}
}

// ---------- 加入/开始/选谱 校验 ----------

// 房间状态/加入校验相关错误。
var (
	ErrNotWhitelisted   = fmt.Errorf("room-not-whitelisted")
	ErrJoinRoomLocked   = fmt.Errorf("join-room-locked")
	ErrJoinGameOngoing  = fmt.Errorf("join-game-ongoing")
	ErrJoinCantMonitor  = fmt.Errorf("join-cant-monitor")
	ErrNoChartSelected  = fmt.Errorf("start-no-chart-selected")
	ErrRoomInvalidState = fmt.Errorf("room-invalid-state")
)

// ValidateJoin 校验用户能否加入（比赛白名单、锁定、状态、观战权限）。
func (r *Room) ValidateJoin(user *User, monitor bool) error {
	if r.Contest != nil {
		if _, ok := r.Contest.Whitelist[user.ID]; !ok {
			return ErrNotWhitelisted
		}
	}
	if r.Locked {
		return ErrJoinRoomLocked
	}
	if _, isWait := r.State.(StateWaitForReady); !monitor && isWait {
		return ErrJoinGameOngoing
	}
	if monitor && !user.CanMonitor() {
		return ErrJoinCantMonitor
	}
	return nil
}

// HandleJoin 处理加入副作用：游戏进行中加入的普通玩家自动计入已完成，不影响本局结束判定。
func (r *Room) HandleJoin(user *User) {
	if st, ok := r.State.(StatePlaying); ok && !user.Monitor {
		st.Aborted[user.ID] = struct{}{}
	}
}

// ValidateStart 校验房主能否开始游戏。
func (r *Room) ValidateStart(user *User) error {
	if err := r.CheckHost(user); err != nil {
		return err
	}
	if r.Chart == nil {
		return ErrNoChartSelected
	}
	if _, ok := r.State.(StateSelectChart); !ok {
		return ErrRoomInvalidState
	}
	return nil
}

// ValidateSelectChart 校验房主能否选谱。
func (r *Room) ValidateSelectChart(user *User) error {
	if err := r.CheckHost(user); err != nil {
		return err
	}
	if _, ok := r.State.(StateSelectChart); !ok {
		return ErrRoomInvalidState
	}
	return nil
}
