package server

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
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
	BroadcastExcept     func(cmd protocol.ServerCommand, exclude map[int]struct{})
	BroadcastToMonitors func(cmd protocol.ServerCommand)
	PickNextHostID      func(ids []int, oldHostID int) (int, bool)
	Lang                *l10n.Language
	Logger              Logger
	DisbandRoom         func(room *Room)
	OnEnterPlaying      func(room *Room)
	OnGameEnd           func(room *Room)
	WSService           WsBroadcaster
	// SystemChatUserID 返回系统聊天消息发送者的 User ID（未配置 SYSTEM_USER_ID 时为 0）。
	// 用于 chat-waiting-reconnect、本局结算等系统消息的发送者标识。
	SystemChatUserID func() int32
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
		r.monitorUsers[user.ID] = user
		r.refreshParticipantsSnapshot()
		return true
	}
	if len(r.users) >= r.MaxUsers && !slices.Contains(r.users, user.ID) {
		return false
	}
	if !slices.Contains(r.users, user.ID) {
		r.users = append(r.users, user.ID)
	}
	r.usersMap[user.ID] = user
	r.refreshParticipantsSnapshot()
	return true
}

// refreshParticipantsSnapshot 重建参与者快照（玩家+观战者的 *User 指针列表）。
// 调用方须持 room.Mu。BroadcastRoom 通过 atomic.Pointer 无锁读取快照，
// 消除 AllParticipantIDs() 的 []int 分配 + state.Users map 查找。
func (r *Room) refreshParticipantsSnapshot() {
	snap := make([]*User, 0, len(r.users)+len(r.monitors))
	for _, u := range r.usersMap {
		snap = append(snap, u)
	}
	for _, u := range r.monitorUsers {
		snap = append(snap, u)
	}
	r.participantsSnapshot.Store(&snap)
}

// ParticipantsSnapshot 返回当前参与者快照（无锁原子读取）。BroadcastRoom 热路径使用。
// 返回的切片由 room 拥有，调用方只读不写；下次刷新后旧切片自然失效。
func (r *Room) ParticipantsSnapshot() []*User {
	if p := r.participantsSnapshot.Load(); p != nil {
		return *p
	}
	return nil
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
	r.Send(lc, protocol.MsgChat{User: int32FromInt(user.ID), Content: content})
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
			u.SetGameTime(math.Inf(-1))
		}
	}
}

// OnUserLeave 处理用户离开房间：广播、移除成员、必要时转移房主，并检查就绪/结算。
// 返回 (shouldDrop, disband)：
//   - shouldDrop：房间已空（无任何成员），调用方直接 delete(state.Rooms) 即可；
//   - disband：比赛 AutoDisband 触发，调用方须在释放 room.Mu 后调 Hub.DisbandRoom
//     以广播离场、转移房主、EmitEvent（直接 delete 会留下悬空引用）。
//
// 两者互斥：shouldDrop=true 时房间已空，CheckAllReady 不会进 checkPlaying，disband 必为 false。
func (r *Room) OnUserLeave(lc *RoomLifecycle, user *User) (shouldDrop bool, disband bool) {
	if lc.Logger != nil && lc.Logger.DebugEnabled() {
		lc.Logger.Debug(fmt.Sprintf("房间 “%s” 用户离开：%s（id=%d，观战=%t，房主=%t）",
			string(r.ID), user.Name, user.ID, user.Monitor, r.HostID == user.ID))
		defer func() {
			lc.Logger.Debug(fmt.Sprintf("房间 “%s” 离开结算：%s，shouldDrop=%t，disband=%t，剩余玩家=%d",
				string(r.ID), user.Name, shouldDrop, disband, len(r.users)))
		}()
	}
	r.Send(lc, protocol.MsgLeaveRoom{User: int32FromInt(user.ID), Name: user.Name})
	user.Room = nil

	if user.Monitor {
		r.monitors = removeInt(r.monitors, user.ID)
		delete(r.monitorUsers, user.ID)
		r.refreshParticipantsSnapshot()
	} else {
		r.users = removeInt(r.users, user.ID)
		delete(r.usersMap, user.ID)
		r.refreshParticipantsSnapshot()
	}

	if r.HostID == user.ID {
		if len(r.users) == 0 {
			return true, false
		}
		newHost, ok := lc.PickNextHostID(r.UserIDs(), user.ID)
		if !ok {
			return true, false
		}
		r.HostID = newHost
		r.logRoomInfo(lc, "log-room-host-changed-offline", map[string]string{
			"old": fmt.Sprintf("%d", user.ID), "next": fmt.Sprintf("%d", newHost),
		})
		r.logHostTransferDetail(lc, user.ID, user.Name, newHost, len(r.users))
		r.Send(lc, protocol.MsgNewHost{User: int32(newHost)})
		if nh := lc.UsersByID(newHost); nh != nil {
			nh.TrySend(protocol.SrvChangeHost{IsHost: true})
		}
	}

	r.NotifyWebSocket(lc)
	disband = r.CheckAllReady(lc)
	return r.IsEmpty(), disband
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

// logRoomMark 以 MARK 级记录房间日志（对齐原版 logRoomMark，用于选谱等标记性事件）。
func (r *Room) logRoomMark(lc *RoomLifecycle, key string, args map[string]string) {
	if lc.Logger == nil {
		return
	}
	if args == nil {
		args = map[string]string{}
	}
	args["room"] = string(r.ID)
	lc.Logger.Mark(l10n.TL(lc.Lang, key, args))
}

// logHostTransferDetail 以 DEBUG 级记录房主离线转移详情（含旧/新房主名与剩余玩家数）。
// 调用方须持 room.Mu。
func (r *Room) logHostTransferDetail(lc *RoomLifecycle, oldID int, oldName string, newID, remaining int) {
	if lc.Logger == nil || !lc.Logger.DebugEnabled() {
		return
	}
	newName := r.nameOf(lc, newID)
	lc.Logger.Debug(fmt.Sprintf("房间 “%s” 房主离线转移：旧房主=%d（%s），新房主=%d（%s），剩余玩家=%d",
		string(r.ID), oldID, oldName, newID, newName, remaining))
}

// joinIntIDs 把整型 ID 列表用 sep 连接成字符串（用于日志中的玩家/观战者列表）。
func joinIntIDs(ids []int, sep string) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, sep)
}

// CheckAllReady 推进房间状态机：
//   - WaitForReady：全员就绪（且非比赛手动开始）则进入 Playing；
//   - Playing：全员完成（成绩或中止）则结算并回到 SelectChart（含比赛自动解散 / cycle 轮换）。
//
// 返回 disband=true 表示比赛 AutoDisband 触发，调用方须在释放 room.Mu 后调 Hub.DisbandRoom
// 完成剩余成员踢出与事件外发（直接 delete 会留下悬空引用）。
func (r *Room) CheckAllReady(lc *RoomLifecycle) (disband bool) {
	switch st := r.State.(type) {
	case StateWaitForReady:
		r.checkWaitForReady(lc, st)
	case StatePlaying:
		return r.checkPlaying(lc, st)
	}
	return false
}

func (r *Room) checkWaitForReady(lc *RoomLifecycle, st StateWaitForReady) {
	if lc.Logger != nil && lc.Logger.DebugEnabled() {
		lc.Logger.Debug(fmt.Sprintf("房间 “%s” 检查就绪：总参与 %d，已就绪 %d，比赛手动开赛=%t",
			string(r.ID), len(r.AllParticipantIDs()), len(st.Started), r.Contest != nil && r.Contest.ManualStart))
	}
	for _, id := range r.AllParticipantIDs() {
		if _, ok := st.Started[id]; !ok {
			return // 还有人未就绪
		}
	}
	if r.Contest != nil && r.Contest.ManualStart {
		if lc.Logger != nil && lc.Logger.DebugEnabled() {
			lc.Logger.Debug(fmt.Sprintf("房间 “%s” 全员就绪但比赛房等待管理员手动开赛", string(r.ID)))
		}
		return // 比赛房：全员就绪后仍等待管理员手动开赛
	}
	if lc.Logger != nil && lc.Logger.DebugEnabled() {
		lc.Logger.Debug(fmt.Sprintf("房间 “%s” 全员就绪，进入 Playing", string(r.ID)))
	}
	r.startPlaying(lc, nil)
}

// startPlaying 把房间切到 Playing 并广播开赛（触发录制钩子、重置 gameTime）。
// 供「全员就绪自动开赛」与「管理员/比赛强制开赛」共用。
// unreadyIDs 是强制开赛时未准备玩家的 ID 列表：若非空，MsgStartPlaying 和
// SrvChangeState(Playing) 都不发给他们，由调用方自行将未准备玩家送回 SelectChart。
func (r *Room) startPlaying(lc *RoomLifecycle, unreadyIDs []int) {
	r.cancelReadyCountdown()
	r.cancelPlayDeadline()
	if lc.OnEnterPlaying != nil {
		lc.OnEnterPlaying(r)
	}
	// 对齐原版：game-start 日志需注入玩家列表与观战者后缀，否则 { $users } 渲染为空。
	sep := ", "
	if lc.Lang != nil && lc.Lang.Tag == "zh-CN" {
		sep = "、"
	}
	monitors := r.MonitorIDs()
	monitorsSuffix := ""
	if len(monitors) > 0 {
		monitorsSuffix = l10n.TL(lc.Lang, "log-room-game-start-monitors", map[string]string{
			"monitors": joinIntIDs(monitors, sep),
		})
	}
	r.logRoomInfo(lc, "log-room-game-start", map[string]string{
		"users":          joinIntIDs(r.UserIDs(), sep),
		"monitorsSuffix": monitorsSuffix,
	})
	// 记录开赛日志。
	if lc.Lang != nil {
		if logText := r.formatMessageForLog(protocol.MsgStartPlaying{}, lc); logText != "" {
			r.AddLog(logText, time.Now().UnixMilli())
		}
	}
	r.ResetGameTime(lc.UsersByID)
	r.State = StatePlaying{
		Results:   make(map[int]config.RecordData),
		Aborted:   make(map[int]struct{}),
		StartedAt: time.Now(),
	}
	// 广播 MsgStartPlaying 和 SrvChangeState(Playing)，只发给已准备的玩家。
	// 强制开赛时未准备玩家不应收到这些包（对齐 Java：未准备玩家回退到 SelectChart 变观战者）。
	startCmd := protocol.SrvMessage{Message: protocol.MsgStartPlaying{}}
	stateCmd := protocol.SrvChangeState{State: r.ClientRoomState()}
	if len(unreadyIDs) > 0 {
		if lc.Logger != nil && lc.Logger.DebugEnabled() {
			lc.Logger.Debug(fmt.Sprintf("房间 “%s” 强制开赛，排除未准备玩家 %v", string(r.ID), unreadyIDs))
		}
		excludeSet := make(map[int]struct{}, len(unreadyIDs))
		for _, id := range unreadyIDs {
			excludeSet[id] = struct{}{}
		}
		lc.BroadcastExcept(startCmd, excludeSet)
		lc.BroadcastExcept(stateCmd, excludeSet)
	} else {
		lc.Broadcast(startCmd)
		lc.Broadcast(stateCmd)
	}
	r.NotifyWebSocket(lc)
}

func (r *Room) checkPlaying(lc *RoomLifecycle, st StatePlaying) (disband bool) {
	finished := true
	var unfinished []int
	for _, id := range r.users {
		_, hasResult := st.Results[id]
		_, hasAbort := st.Aborted[id]
		if !hasResult && !hasAbort {
			finished = false
			unfinished = append(unfinished, id)
		}
	}
	if lc.Logger != nil && lc.Logger.DebugEnabled() {
		lc.Logger.Debug(fmt.Sprintf("房间 “%s” 检查对局：玩家 %d，已结算 %d，已放弃 %d，未完成 %v",
			string(r.ID), len(r.users), len(st.Results), len(st.Aborted), unfinished))
	}
	if !finished {
		r.notifyDanglingReconnect(lc, &st)
		return false
	}

	r.broadcastGameRanking(lc, st)
	r.logRoomInfo(lc, "log-room-game-end", map[string]string{
		"uploaded": fmt.Sprintf("%d", len(st.Results)),
		"aborted":  fmt.Sprintf("%d", len(st.Aborted)),
	})
	r.Send(lc, protocol.MsgGameEnd{})
	if lc.OnGameEnd != nil {
		lc.OnGameEnd(r)
	}
	r.cancelPlayDeadline()
	r.State = StateSelectChart{}

	// 比赛 AutoDisband：不再在此调 lc.DisbandRoom（会自死锁，因调用方持 room.Mu）。
	// 返回 true 让调用方在 room.Mu 释放后执行 DisbandRoom。
	if r.Contest != nil && r.Contest.AutoDisband {
		if lc.Logger != nil && lc.Logger.DebugEnabled() {
			lc.Logger.Debug(fmt.Sprintf("房间 “%s” 比赛 AutoDisband，返回 true 让调用方解散", string(r.ID)))
		}
		return true
	}

	if r.IsCycle() && len(r.users) > 1 {
		if lc.Logger != nil && lc.Logger.DebugEnabled() {
			lc.Logger.Debug(fmt.Sprintf("房间 “%s” cycle 模式轮换房主", string(r.ID)))
		}
		r.rotateCycleHost(lc)
	}

	r.OnStateChange(lc)
	r.NotifyWebSocket(lc)
	return false
}

// notifyDanglingReconnect 在「其他玩家都已完成、仅剩断线挂起玩家未完成」时，向房间播报
// 一次「正在等待重连 + 剩余倒计时」提示（每名挂起玩家仅播报一次）。
//
// 调用方须持 state.Mu。
func (r *Room) notifyDanglingReconnect(lc *RoomLifecycle, st *StatePlaying) {
	var unfinished, dangling []int
	for _, id := range r.users {
		_, hasResult := st.Results[id]
		_, hasAbort := st.Aborted[id]
		if hasResult || hasAbort {
			continue
		}
		unfinished = append(unfinished, id)
		u := lc.UsersByID(id)
		if u == nil {
			dangling = append(dangling, id)
			continue
		}
		sessionMissing := u.Session() == nil
		if sessionMissing {
			dangling = append(dangling, id)
		}
	}
	if len(unfinished) == 0 || len(dangling) != len(unfinished) {
		return
	}
	if st.ReconnectNotified == nil {
		st.ReconnectNotified = make(map[int]struct{})
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
		remainMs := u.dangleDeadlineMs(time.Now().UnixMilli())
		seconds := max(1, int(math.Ceil(float64(remainMs)/1000)))
		r.Send(lc, protocol.MsgChat{User: lc.SystemChatUserID(), Content: l10n.TL(lc.Lang, "chat-waiting-reconnect",
			map[string]string{"user": u.Name, "seconds": fmt.Sprintf("%d", seconds)})})
	}
}

// broadcastGameRanking 计算并播报本局排名（按分数降序，平局取 id 升序以保证确定性）。
// 单人游玩（仅一份成绩）不输出排名。成绩字段格式对齐 dispatch.go 的 buildChatRecordMap：
// score 原值、acc 取 math.Round(accuracy*100) 保留两位、std 取 math.Round(*1000) 毫秒。
func (r *Room) broadcastGameRanking(lc *RoomLifecycle, st StatePlaying) {
	if len(st.Results) <= 1 {
		return
	}
	ids := make([]int, 0, len(st.Results))
	for id := range st.Results {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b int) int {
		sa, sb := st.Results[a].Score, st.Results[b].Score
		if sa != sb {
			return sb - sa // 分数降序
		}
		return a - b // 平局 id 升序
	})

	var b strings.Builder
	b.WriteByte('\n')
	b.WriteString(strings.Repeat("=", 72))
	b.WriteByte('\n')
	b.WriteString(l10n.TL(lc.Lang, "chat-game-ranking-title", nil))
	b.WriteByte('\n')
	for i, id := range ids {
		res := st.Results[id]
		args := map[string]string{
			"rank":  strconv.Itoa(i + 1),
			"name":  r.nameOf(lc, id),
			"score": strconv.Itoa(res.Score),
			"acc":   fmt.Sprintf("%.2f", math.Round(res.Accuracy*100)),
		}
		if res.Std != nil {
			args["hasStd"] = "true"
			args["std"] = fmt.Sprintf("%d", int(math.Round(*res.Std*1000)))
		} else {
			args["hasStd"] = "false"
		}
		b.WriteString(l10n.TL(lc.Lang, "chat-game-ranking-line", args))
		if i < len(ids)-1 {
			b.WriteByte('\n')
		}
	}
	r.Send(lc, protocol.MsgChat{User: lc.SystemChatUserID(), Content: b.String()})
}

// rotateCycleHost 在 cycle 模式下把房主轮换到下一位。对齐 jphira-mp 的
// transferHostToNextPlayer：按 ID 升序找大于当前房主 ID 的最小者，没有则回环到最小 ID。
//
// 若管理员通过 CLI 指定了下一轮房主（nextHostID）且该用户仍在房间内、且不是当前房主，
// 则使用指定 ID；否则回退到默认轮换。无论命中与否，nextHostID 都被一次性消费。
func (r *Room) rotateCycleHost(lc *RoomLifecycle) {
	oldHost := r.HostID
	var newHost int
	designated := false
	if r.nextHostID != nil {
		cand := *r.nextHostID
		r.nextHostID = nil
		if cand != oldHost && r.ContainsUser(cand) {
			newHost = cand
			designated = true
			if lc.Logger != nil && lc.Logger.DebugEnabled() {
				lc.Logger.Debug(fmt.Sprintf("房间 “%s” cycle 房主轮换：使用指定 nextHostID=%d（命中）", string(r.ID), cand))
			}
		} else {
			if lc.Logger != nil && lc.Logger.DebugEnabled() {
				reason := "未知"
				if cand == oldHost {
					reason = "与当前房主相同"
				} else if !r.ContainsUser(cand) {
					reason = "指定用户不在房间内"
				}
				lc.Logger.Debug(fmt.Sprintf("房间 “%s” cycle 房主轮换：指定 nextHostID=%d 未命中（%s），回退默认轮换", string(r.ID), cand, reason))
			}
		}
	}
	if !designated {
		next, ok := lc.PickNextHostID(r.UserIDs(), oldHost)
		if !ok {
			if lc.Logger != nil && lc.Logger.DebugEnabled() {
				lc.Logger.Debug(fmt.Sprintf("房间 “%s” cycle 房主轮换：PickNextHostID 无候选，放弃轮换", string(r.ID)))
			}
			return
		}
		newHost = next
	}
	r.HostID = newHost
	if lc.Logger != nil && lc.Logger.DebugEnabled() {
		lc.Logger.Debug(fmt.Sprintf("房间 “%s” cycle 房主轮换结果：%d → %d", string(r.ID), oldHost, newHost))
	}
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
	if monitor && !user.CanMonitor() {
		return ErrJoinCantMonitor
	}
	return nil
}

// HandleJoin 处理加入副作用：游戏进行中加入的普通玩家自动计入已完成，不影响本局结束判定。
// 调用方须持 room.Mu。lc 用于在副作用发生时输出本地化日志，调用方应传入
// 房间对应的 RoomLifecycle（一般由 Hub.MakeRoomLifecycle(room) 提供）。
func (r *Room) HandleJoin(lc *RoomLifecycle, user *User) {
	st, ok := r.State.(StatePlaying)
	if !ok || user.Monitor {
		return
	}
	if _, already := st.Aborted[user.ID]; !already {
		st.Aborted[user.ID] = struct{}{}
		// 显式日志：admin 路径与控制台可观察到「中途加入、计入已中止」事件，
		// 避免静默改变本局结束条件。原版通过 l10n 消息 "log-user-join-late" 表达。
		r.logRoomInfo(lc, "log-user-join-late", map[string]string{
			"user": user.Name, "room": string(r.ID),
		})
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
