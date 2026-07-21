// hub_room_ops.go 把 Hub 对外暴露的「房间级操作入口」从 hub.go 拆出：
// 建房 / 加入 / 解散 / 回放假观战者与迟到提示。Hub 整体编排设计见 hub.go。
package server

import (
	"fmt"

	"github.com/Pimeng/gooophira-mp/internal/common/platform/l10n"
	"github.com/Pimeng/gooophira-mp/internal/common/protocol"
)

// ProcessCreateRoom 处理建房：封禁/维护/开关/已在房 校验，占用与数量上限检查，建房并广播。
func (h *Hub) ProcessCreateRoom(user *User, id protocol.RoomID) (err error) {
	defer func() {
		if err != nil && h.State.Logger != nil && h.State.Logger.DebugEnabled() {
			h.State.Logger.Debug(fmt.Sprintf("建房失败：用户“%s”，房间ID=%s，原因=%v", user.Name, string(id), err))
		}
	}()
	if h.isBanned(user) {
		return errUserBanned
	}
	if h.State.Maintenance {
		return errMaintenance
	}
	if !h.State.RoomCreationEnabled {
		return errRoomCreationDisabled
	}
	if user.Room != nil {
		return errAlreadyInRoom
	}

	if _, occupied := h.State.Rooms[id]; occupied {
		return errCreateIDOccupied
	}
	if maxRooms := h.State.Config.EffectiveMaxRooms(); maxRooms >= 1 && len(h.State.Rooms) >= maxRooms {
		return errRoomsLimitReached
	}
	room := NewRoom(id, user.ID, h.State.Config.EffectiveRoomMaxUsers(), h.State.ReplayEnabled)
	h.State.Rooms[id] = room
	user.Room = room

	room.Mu.Lock()
	// NewRoom 只把 hostID 放入 users 切片（无 *User 指针），这里补全 usersMap 并刷新快照，
	// 否则 BroadcastRoom 的 ParticipantsSnapshot 会漏掉房主。
	room.usersMap[user.ID] = user
	room.refreshParticipantsSnapshot()
	room.RefreshLive(h.State.ReplayEnabled)
	// 对齐原版：建房时输出 MARK 级控制台日志。
	lc := h.MakeRoomLifecycle(room)
	room.logRoomMark(lc, "log-room-created", map[string]string{"user": user.Name})
	h.BroadcastRoomMessage(room, protocol.MsgCreateRoom{User: int32FromInt(user.ID)})
	room.EmitUserEvent(h.State, EventRoomCreate, user)
	h.sendFakeMonitorJoin(user, room)
	room.Mu.Unlock()
	return nil
}

// sendReplayRecorderHint 向用户发送一条系统聊天（MsgChat User=SYSTEM_USER_ID），告知其
// 刚才加入的假观战者是服务器模拟的回放采集会话，仅供录制使用，不参与游戏，也不影响对局
// 结果。仅在确实派发假观战者加入消息后调用——避免裸提示让玩家误以为有真实观战者进入。
// name 为提示聊天中指代该会话的名称，与假观战者在进出包中的显示名一致（未配置时取
// replay-recorder-name，配置真实 ID 后取 bot 真实昵称）。配置真实 ID 后假观战者与系统
// 聊天发送者共用 bot 身份，进出包与聊天发送者前缀均显示为 bot 昵称。
// 迟到加入者会单独收到 chat-late-join-hint 提示（见 sendLateJoinHint），与本提示解耦。
func (h *Hub) sendReplayRecorderHint(user *User, lang *l10n.Language) {
	if user == nil || lang == nil {
		return
	}
	hint, ok := tlOrSkip(lang, "chat-replay-recorder-hint", nil)
	if !ok {
		return
	}
	user.TrySend(protocol.SrvMessage{Message: protocol.MsgChat{User: h.State.SystemChatUserID(), Content: hint}})
}

// sendLateJoinHint 向迟到加入者（对局进行中加入的非观战者）发送系统聊天提示：告知其
// 本局已自动计为已放弃、无需操作、不影响分数，下一局可正常参与。与回放假观战者提示
// 解耦——即使未启用回放录制，迟到加入者仍会收到本提示。
//
// 派发走 ProtocolHack 延迟调度：① 避免在 room.Mu 内同步写 chat 通道；② 让真实玩家
// 加入广播（BroadcastRoom OnJoinRoom / MsgJoinRoom）先抵达客户端，提示紧随其后到达，
// 体感更自然、避免客户端在加入动画途中就弹出系统聊天。
func (h *Hub) sendLateJoinHint(user *User, lang *l10n.Language) {
	if user == nil || lang == nil {
		return
	}
	hint, ok := tlOrSkip(lang, "chat-late-join-hint", nil)
	if !ok {
		return
	}
	snapshot := user
	sysID := h.State.SystemChatUserID()
	h.NewProtocolHack().schedule(func() {
		snapshot.TrySend(protocol.SrvMessage{Message: protocol.MsgChat{User: sysID, Content: hint}})
	})
}

// sendFakeMonitorJoin 向目标用户发送回放假观战者加入通知（OnJoinRoom + JoinRoom）。
// 客户端检测到观战者后会上报 Touches/Judges，供录制器采集。对应 TS Session.sendFakeMonitorJoin。
//
// 实现走 ProtocolHack.forceSyncInfo：默认延迟 10ms（可经 -protocol-hack-delay 调整），
// 模仿 TS setImmediate 语义：客户端收到 OnJoinRoom 时房间必须已初始化完毕，否则客户端
// 不会把假观战者加入其内部用户列表。
//
// 迟到加入者的提示（chat-late-join-hint）由 ProcessJoinRoom 在 HandleJoin 后单独发送，
// 与本函数解耦——未启用回放录制时迟到加入者仍会收到迟到提示。
func (h *Hub) sendFakeMonitorJoin(targetUser *User, room *Room) {
	if !h.State.ReplayEnabled || !room.ReplayEligible {
		return
	}
	// 仅在用户仍在此房间时发送；已离开或已换房则跳过。
	roomID := room.ID
	state := h.State
	// 锁定到发送时刻的房间 ID 与 user 指针快照，避免延迟期间 room 被换或 user 被注销导致
	// 误发送到错误目标。ProtocolHack 内部仍会走标准 TrySend 路径，无活跃会话则 no-op。
	snapshot := targetUser
	h.NewProtocolHack().schedule(func() {
		state.Mu.Lock()
		currentRoom := snapshot.Room
		state.Mu.Unlock()
		if currentRoom == nil || currentRoom.ID != roomID {
			return
		}
		if state.ReplayRecorder == nil {
			return
		}
		// 假观战者身份：未配置 SYSTEM_USER_ID 时用固定 ID + 本地化名「回放录制器（系统）」；
		// 配置真实 ID 后用该 bot 真实身份（异步拉取昵称，拉取完成前用本地化名兜底）。提示聊天
		// name 变量用 fake.Name，确保进出包名与提示内容指代一致。
		fallbackName := l10n.TL(state.ServerLang, "replay-recorder-name", nil)
		fake := state.ReplayRecorder.FakeMonitorInfo(fallbackName)
		snapshot.TrySend(protocol.SrvOnJoinRoom{Info: fake})
		snapshot.TrySend(protocol.SrvMessage{
			Message: protocol.MsgJoinRoom{User: fake.ID, Name: fake.Name},
		})
		// 紧跟一条系统聊天，明确告知玩家这是服务器模拟的回放采集会话、无需理会，
		// 避免其误以为有真实观战者进入并产生困惑或等待行为。
		h.sendReplayRecorderHint(snapshot, state.ServerLang)
	})
}

// ProcessJoinRoom 处理加入房间：封禁/维护/已在房 校验，房间封禁/存在/加入合法性检查，
// 加入并广播 OnJoinRoom + JoinRoom 消息，返回 JoinRoomResponse。
func (h *Hub) ProcessJoinRoom(user *User, id protocol.RoomID, monitor bool) (resp protocol.JoinRoomResponse, err error) {
	defer func() {
		if err != nil && h.State.Logger != nil && h.State.Logger.DebugEnabled() {
			h.State.Logger.Debug(fmt.Sprintf("加入房间失败：用户“%s”，房间ID=%s，观战=%t，原因=%v", user.Name, string(id), monitor, err))
		}
	}()
	var zero protocol.JoinRoomResponse
	if h.isBanned(user) {
		return zero, errUserBanned
	}
	if h.State.Maintenance {
		return zero, errMaintenance
	}
	if user.Room != nil {
		return zero, errAlreadyInRoom
	}

	if banned := h.State.BannedRoomUsers[id]; banned != nil {
		if _, ok := banned[user.ID]; ok {
			return zero, errRoomBanned
		}
	}
	room := h.State.Rooms[id]
	if room == nil {
		return zero, errRoomNotFound
	}

	room.Mu.Lock()
	if err := room.ValidateJoin(user, monitor); err != nil {
		room.Mu.Unlock()
		return zero, err
	}
	if !room.AddUser(user, monitor) {
		room.Mu.Unlock()
		return zero, errJoinRoomFull
	}
	user.Monitor = monitor
	user.Room = room
	lc := h.MakeRoomLifecycle(room)
	room.HandleJoin(lc, user)
	// 迟到加入者（对局进行中加入的非观战者）单独发送一条提示，告知本局已自动计为已放弃。
	// 此提示与回放假观战者解耦：即使未启用回放录制，迟到加入者仍会收到本提示。
	if !monitor {
		if sp, ok := room.State.(StatePlaying); ok {
			if _, aborted := sp.Aborted[user.ID]; aborted {
				h.sendLateJoinHint(user, h.State.ServerLang)
			}
		}
	}
	room.RefreshLive(h.State.ReplayEnabled)

	// 对齐原版：加入房间输出 MARK 级控制台日志（观战者带后缀）。
	room.logRoomMark(lc, "log-room-joined", map[string]string{
		"user": user.Name, "suffix": h.monitorSuffix(monitor),
	})
	h.BroadcastRoom(room, protocol.SrvOnJoinRoom{Info: user.ToInfo()})
	h.BroadcastRoomMessage(room, protocol.MsgJoinRoom{User: int32FromInt(user.ID), Name: user.Name})
	h.sendFakeMonitorJoin(user, room)
	room.EmitUserEvent(h.State, EventUserJoin, user)

	users := make([]protocol.UserInfo, 0, room.UserCount()+room.MonitorCount())
	for _, pid := range room.AllParticipantIDs() {
		if u := h.State.Users[pid]; u != nil {
			users = append(users, u.ToInfo())
		}
	}

	// ProtocolHack：非选谱态但已有谱面时，响应里伪装成 SelectChart 让客户端先获知谱面 ID。
	respState := room.clientRoomStateForJoin()
	room.Mu.Unlock()

	return protocol.JoinRoomResponse{State: respState, Users: users, Live: room.IsLive()}, nil
}

// DisbandRoom 解散房间：让所有成员离开并从全局移除。调用方须持 state.Mu。
// 内部循环 OnUserLeave 时房间状态已是 StateSelectChart（被 checkPlaying 切回），
// 故返回的 disband 恒 false，可忽略。
func (h *Hub) DisbandRoom(room *Room) {
	// 显式取消「准备倒计时」与「结算超时」：避免房间解散后延迟回调仍持有 room 指针，
	// 也避免房间 ID 被复用时回调误广播到新房间。
	room.cancelReadyCountdown()
	room.cancelPlayDeadline()
	lc := h.MakeRoomLifecycle(room)
	room.Mu.Lock()
	for _, u := range room.ParticipantsSnapshot() {
		if u == nil || u.Room == nil || u.Room.ID != room.ID {
			continue
		}
		_, _ = room.OnUserLeave(lc, u)
	}
	delete(h.State.Rooms, room.ID)
	room.Mu.Unlock()
	room.EmitEvent(h.State, Event{Type: EventRoomDisband})
}
