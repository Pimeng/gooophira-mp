package server

import (
	"errors"
	"math/rand/v2"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

// Hub 把 ServerState 与上游依赖（Phira API、回放、观战缓冲、状态钩子）组合为命令派发
// 的编排层。所有房间操作都经 Session 接口（User.TrySend）发送，因而无需依赖 network 包，
// 可用 mock 完整单测。
//
// 并发模型：TS 靠单线程事件循环串行化所有命令。Go 中由 network 层在调用
// ProcessClientCommand 前后持有全局 ServerState.Mu，把命令处理整体串行化（等价于 TS 事件
// 循环）。因此 Hub 方法内部**不再加锁**（调用方已持锁）；广播只向各会话的发送通道入队
// （非阻塞），真正的 socket 写在各自的 writer goroutine 中、锁外完成，避免持锁阻塞 I/O。
// 认证涉及阻塞的 Phira HTTP 请求，必须在锁外完成，仅在注册用户的瞬间短暂持锁。
type Hub struct {
	State *ServerState
	Phira PhiraAPI

	// OnEnterPlaying 进入 Playing 时回调（启动回放录制）。可为 nil。
	OnEnterPlaying func(room *Room)
	// OnGameEnd 一局结束时回调（结算 / 自动上传）。可为 nil。
	OnGameEnd func(room *Room)
	// Monitor 观战数据聚合缓冲（可为 nil = 直接广播给观战者）。
	Monitor MonitorBuffer
}

// NewHub 创建编排层。
func NewHub(state *ServerState, phira PhiraAPI) *Hub {
	return &Hub{State: state, Phira: phira}
}

func pickRandom(ids []int) (int, bool) {
	if len(ids) == 0 {
		return 0, false
	}
	return ids[rand.IntN(len(ids))], true
}

// 派发与房间操作相关的错误（message 即 l10n key，errToStr 时按用户语言本地化）。
var (
	errUserBanned           = errors.New("user-banned-by-server")
	errMaintenance          = errors.New("server-maintenance")
	errRoomCreationDisabled = errors.New("room-creation-disabled")
	errAlreadyInRoom        = errors.New("room-already-in-room")
	errCreateIDOccupied     = errors.New("create-id-occupied")
	errRoomsLimitReached    = errors.New("rooms-limit-reached")
	errRoomBanned           = errors.New("room-banned")
	errRoomNotFound         = errors.New("room-not-found")
	errJoinRoomFull         = errors.New("join-room-full")
	errNoRoom               = errors.New("room-no-room")
	errAuthRepeated         = errors.New("auth-repeated-authenticate")
	errRecordInvalid        = errors.New("record-invalid")
	errRecordChartMismatch  = errors.New("record-chart-mismatch")
	errAlreadyReady         = errors.New("room-already-ready")
	errNotReady             = errors.New("room-not-ready")
	errGameAborted          = errors.New("room-game-aborted")
	errRecordUploaded       = errors.New("record-already-uploaded")
	errContestNotFound      = errors.New("contest-room-not-found")
	errRoomNotWaiting       = errors.New("room-not-waiting")
	errNoChartSelected      = errors.New("no-chart-selected")
	errNotAllReady          = errors.New("not-all-ready")
	errUserMustDisconnect   = errors.New("user-must-be-disconnected")
	errUserNotInRoom        = errors.New("user-not-in-room")
	errCannotMovePlaying    = errors.New("cannot-move-while-playing")
	errTargetRoomNotIdle    = errors.New("target-room-not-idle")
)

// ---------- 广播 ----------

// BroadcastRoom 向房间所有参与者发送一条命令。
func (h *Hub) BroadcastRoom(room *Room, cmd protocol.ServerCommand) {
	for _, id := range room.AllParticipantIDs() {
		if u := h.State.Users[id]; u != nil {
			u.TrySend(cmd)
		}
	}
}

func (h *Hub) broadcastToMonitors(room *Room, cmd protocol.ServerCommand) {
	for _, id := range room.MonitorIDs() {
		if u := h.State.Users[id]; u != nil {
			u.TrySend(cmd)
		}
	}
}

// BroadcastRoomMessage 经房间 Send 广播一条 Message（含房间日志记录）。
func (h *Hub) BroadcastRoomMessage(room *Room, msg protocol.Message) {
	room.Send(h.MakeRoomLifecycle(room), msg)
}

// monitorSuffix 返回观战者后缀（对齐原版 label-monitor-suffix），非观战者为空串。
func (h *Hub) monitorSuffix(monitor bool) string {
	if !monitor {
		return ""
	}
	return l10n.TL(h.State.ServerLang, "label-monitor-suffix", nil)
}

// MakeRoomLifecycle 为房间构造生命周期依赖注入。
func (h *Hub) MakeRoomLifecycle(room *Room) *RoomLifecycle {
	return &RoomLifecycle{
		UsersByID:           func(id int) *User { return h.State.Users[id] },
		Broadcast:           func(cmd protocol.ServerCommand) { h.BroadcastRoom(room, cmd) },
		BroadcastToMonitors: func(cmd protocol.ServerCommand) { h.broadcastToMonitors(room, cmd) },
		PickRandomUserID:    pickRandom,
		Lang:                h.State.ServerLang,
		Logger:              h.State.Logger,
		DisbandRoom:         h.DisbandRoom,
		OnEnterPlaying:      h.OnEnterPlaying,
		OnGameEnd:           h.OnGameEnd,
		WSService:           h.State.WSService,
	}
}

// RequireRoom 返回用户所在房间，不在任何房间则返回 errNoRoom。
func (h *Hub) RequireRoom(user *User) (*Room, error) {
	if user.Room == nil {
		return nil, errNoRoom
	}
	return user.Room, nil
}

// CheckRoomAllReady 推进房间状态机（就绪/结算检查）。
func (h *Hub) CheckRoomAllReady(room *Room) {
	room.CheckAllReady(h.MakeRoomLifecycle(room))
}

func (h *Hub) isBanned(user *User) bool {
	_, banned := h.State.BannedUsers[user.ID]
	return banned
}

// ---------- 房间生命周期操作 ----------

// ProcessCreateRoom 处理建房：封禁/维护/开关/已在房 校验，占用与数量上限检查，建房并广播。
func (h *Hub) ProcessCreateRoom(user *User, id protocol.RoomID) error {
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

	room.RefreshLive(h.State.ReplayEnabled)
	// 对齐原版：建房时输出 MARK 级控制台日志。
	room.logRoomMark(h.MakeRoomLifecycle(room), "log-room-created", map[string]string{"user": user.Name})
	h.BroadcastRoomMessage(room, protocol.MsgCreateRoom{User: int32(user.ID)})
	h.State.EmitEvent(Event{Type: EventRoomCreate, RoomID: room.ID.String(), UserID: user.ID, UserName: user.Name})
	h.sendFakeMonitorJoin(user, room)
	return nil
}

// sendFakeMonitorJoin 向目标用户发送回放假观战者加入通知（OnJoinRoom + JoinRoom）。
// 客户端检测到观战者后会上报 Touches/Judges，供录制器采集。对应 TS Session.sendFakeMonitorJoin。
//
// ⚠️ 必须延迟到当前命令处理完成后发送（模仿 TS setImmediate）：客户端收到 OnJoinRoom
// 时房间必须已初始化完毕，否则客户端不会把假观战者加入其内部用户列表，导致不会上报
// Touches/Judges，回放文件将只有元数据而无任何帧。
func (h *Hub) sendFakeMonitorJoin(targetUser *User, room *Room) {
	if !h.State.ReplayEnabled || !room.ReplayEligible {
		return
	}
	// 对齐 TS sendFakeMonitorJoin：延迟到下一轮事件循环，确保 CreateRoom/JoinRoom
	// 的响应已先被客户端处理完毕。同时再验证用户仍在此房间（可能在延迟期间离开）。
	roomID := room.ID
	time.AfterFunc(20*time.Millisecond, func() {
		// 仅在用户仍在此房间时发送；已离开或已换房则跳过。
		if targetUser.Room == nil || targetUser.Room.ID != roomID {
			return
		}
		if h.State.ReplayRecorder == nil {
			return
		}
		name := l10n.TL(h.State.ServerLang, "replay-recorder-name", nil)
		fake := h.State.ReplayRecorder.FakeMonitorInfo(name)
		targetUser.TrySend(protocol.SrvOnJoinRoom{Info: fake})
		targetUser.TrySend(protocol.SrvMessage{
			Message: protocol.MsgJoinRoom{User: fake.ID, Name: fake.Name},
		})
	})
}

// ProcessJoinRoom 处理加入房间：封禁/维护/已在房 校验，房间封禁/存在/加入合法性检查，
// 加入并广播 OnJoinRoom + JoinRoom 消息，返回 JoinRoomResponse。
func (h *Hub) ProcessJoinRoom(user *User, id protocol.RoomID, monitor bool) (protocol.JoinRoomResponse, error) {
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

	if err := room.ValidateJoin(user, monitor); err != nil {
		return zero, err
	}
	if !room.AddUser(user, monitor) {
		return zero, errJoinRoomFull
	}
	user.Monitor = monitor
	user.Room = room
	room.HandleJoin(user)
	room.RefreshLive(h.State.ReplayEnabled)

	// 对齐原版：加入房间输出 MARK 级控制台日志（观战者带后缀）。
	room.logRoomMark(h.MakeRoomLifecycle(room), "log-room-joined", map[string]string{
		"user": user.Name, "suffix": h.monitorSuffix(monitor),
	})
	h.BroadcastRoom(room, protocol.SrvOnJoinRoom{Info: user.ToInfo()})
	h.BroadcastRoomMessage(room, protocol.MsgJoinRoom{User: int32(user.ID), Name: user.Name})
	h.sendFakeMonitorJoin(user, room)
	h.State.EmitEvent(Event{Type: EventUserJoin, RoomID: room.ID.String(), UserID: user.ID, UserName: user.Name, UserCount: room.UserCount()})

	users := make([]protocol.UserInfo, 0, room.UserCount()+room.MonitorCount())
	for _, pid := range room.AllParticipantIDs() {
		if u := h.State.Users[pid]; u != nil {
			users = append(users, u.ToInfo())
		}
	}

	// ProtocolHack：非选谱态但已有谱面时，响应里伪装成 SelectChart 让客户端先获知谱面 ID。
	respState := room.ClientRoomState()
	if _, isSelect := room.State.(StateSelectChart); !isSelect && room.Chart != nil {
		cid := int32(room.Chart.ID)
		respState = protocol.RoomStateSelectChart{ID: &cid}
	}

	return protocol.JoinRoomResponse{State: respState, Users: users, Live: room.IsLive()}, nil
}

// DisbandRoom 解散房间：让所有成员离开并从全局移除。
func (h *Hub) DisbandRoom(room *Room) {
	lc := h.MakeRoomLifecycle(room)
	for _, id := range room.AllParticipantIDs() {
		u := h.State.Users[id]
		if u == nil || u.Room == nil || u.Room.ID != room.ID {
			continue
		}
		room.OnUserLeave(lc, u)
	}
	delete(h.State.Rooms, room.ID)
	h.State.EmitEvent(Event{Type: EventRoomDisband, RoomID: room.ID.String()})
}

// ---------- Phira 取数 ----------

// FetchChart 取谱面（TODO stage-4: 加 chartCache）。
func (h *Hub) FetchChart(user *User, id int) (config.Chart, error) {
	if h.Phira == nil {
		return config.Chart{}, errors.New("chart-fetch-failed")
	}
	return h.Phira.FetchChart(id)
}

// FetchRecord 取成绩（TODO stage-4: 加 recordCache）。
func (h *Hub) FetchRecord(user *User, id int) (config.RecordData, error) {
	if h.Phira == nil {
		return config.RecordData{}, errors.New("record-fetch-failed")
	}
	return h.Phira.FetchRecord(id)
}
