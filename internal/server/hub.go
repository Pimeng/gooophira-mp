package server

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

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
	// ctx 是服务器级根 context，派生给上游 HTTP 调用（Phira API）。
	// 关闭时取消，让进行中的请求及时返回。nil 时回退到 context.Background()。
	ctx context.Context

	// OnEnterPlaying 进入 Playing 时回调（启动回放录制）。可为 nil。
	OnEnterPlaying func(room *Room)
	// OnGameEnd 一局结束时回调（结算 / 自动上传）。可为 nil。
	OnGameEnd func(room *Room)
	// Monitor 观战数据聚合缓冲（可为 nil = 直接广播给观战者）。
	Monitor MonitorBuffer
}

// NewHub 创建编排层。Hub.ctx 默认为 context.Background()；
// 调用方可经 SetContext 注入服务器级根 ctx（关闭时取消以中断进行中的上游 HTTP 请求）。
func NewHub(state *ServerState, phira PhiraAPI) *Hub {
	return &Hub{State: state, Phira: phira, ctx: context.Background()}
}

// SetContext 设置 Hub 的根 ctx（用于派生上游 HTTP 调用的 context）。
// 通常在 main.go 中创建 rootCtx 后立即调用。nil 视为 context.Background()。
// 幂等：可多次调用，后调用覆盖前值。
func (h *Hub) SetContext(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	h.ctx = ctx
}

// ctxWithTimeout 从 Hub 的根 ctx 派生一个带超时的 ctx，用于上游 HTTP 调用。
func (h *Hub) ctxWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(h.ctx, d)
}

// pickNextHost 对齐 jphira-mp 的 PlayerManager.transferHostToNextPlayer：
// 按用户 ID 升序排序，选出 ID 大于 oldHostID 的最小者；若没有则回环到最小 ID。
// 完全确定性，用于离线房主转移与 cycle 轮转两种场景。
func pickNextHost(ids []int, oldHostID int) (int, bool) {
	if len(ids) == 0 {
		return 0, false
	}
	sorted := append([]int(nil), ids...)
	sort.Ints(sorted)
	for _, id := range sorted {
		if id > oldHostID {
			return id, true
		}
	}
	return sorted[0], true
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
	ErrUserNotInRoom        = errors.New("user-not-in-room")
	errCannotMovePlaying    = errors.New("cannot-move-while-playing")
	errTargetRoomNotIdle    = errors.New("target-room-not-idle")
)

// ---------- 广播 ----------

// BroadcastRoom 向房间所有参与者发送一条命令（预编码一次，广播给所有用户）。
// 用 room.ParticipantsSnapshot() 无锁读取参与者列表，消除 AllParticipantIDs()
// 的 []int 分配 + state.Users map 查找。
func (h *Hub) BroadcastRoom(room *Room, cmd protocol.ServerCommand) {
	users := room.ParticipantsSnapshot()
	if len(users) == 0 {
		return
	}
	// 预编码一次帧，通过 TrySendFrameOwned 广播给所有用户（encodeServerCommandFrame
	// 输出的是新建切片，调用方拥有所有权，可省一次 copy）。
	frame := encodeServerCommandFrame(cmd)
	if frame == nil {
		return
	}
	for _, u := range users {
		u.TrySendFrameOwned(frame)
	}
}

// BroadcastRoomExcept 向房间内除 exclude 中用户外的所有在线参与者发送命令。
func (h *Hub) BroadcastRoomExcept(room *Room, cmd protocol.ServerCommand, exclude map[int]struct{}) {
	users := room.ParticipantsSnapshot()
	if len(users) == 0 {
		return
	}
	frame := encodeServerCommandFrame(cmd)
	if frame == nil {
		return
	}
	for _, u := range users {
		if _, skip := exclude[u.ID]; skip {
			continue
		}
		u.TrySendFrameOwned(frame)
	}
}

func (h *Hub) broadcastToMonitors(room *Room, cmd protocol.ServerCommand) {
	users := room.MonitorUsers()
	if len(users) == 0 {
		return
	}
	frame := encodeServerCommandFrame(cmd)
	if frame == nil {
		return
	}
	for _, u := range users {
		u.TrySendFrameOwned(frame)
	}
}

// encodeServerCommandFrame 编码一条服务端命令为二进制帧（用于广播预编码优化）。
// 返回的帧为新建切片（已从对象池拷贝出），调用方拥有所有权，可直接走 TrySendFrameOwned。
func encodeServerCommandFrame(cmd protocol.ServerCommand) []byte {
	w := serverFrameWriterPool.Get().(*protocol.BinaryWriter)
	defer serverFrameWriterPool.Put(w)
	w.Reset()
	protocol.EncodeServerCommand(w, cmd)
	fb := w.ToFrameBuffer()
	frame := make([]byte, len(fb))
	copy(frame, fb)
	return frame
}

// serverFrameWriterPool 是临时 BinaryWriter 对象池（预留 5 字节 LEB128 头部）。
var serverFrameWriterPool = &sync.Pool{
	New: func() any { return protocol.NewFrameWriter(5) },
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

// MakeRoomLifecycle 为房间构造生命周期依赖注入。结果缓存在 room.lifecycle 上，
// 因为 RoomLifecycle 捕获的 h 和 room 在房间生命周期内不变——每次命令都重建
// 会产生 4 个闭包 + 结构体的堆分配（room-cycle 场景下占 2.9 GB 总分配）。
func (h *Hub) MakeRoomLifecycle(room *Room) *RoomLifecycle {
	if lc := room.lifecycle.Load(); lc != nil {
		return lc
	}
	lc := &RoomLifecycle{
		State: h.State,
		// UsersByID 从 room.usersMap 查找（持 room.Mu 时安全），避免读全局
		// state.Users 引入 data race（room-only 命令持 room.Mu 而非 state.Mu）。
		UsersByID:           func(id int) *User { return room.usersMap[id] },
		Broadcast:           func(cmd protocol.ServerCommand) { h.BroadcastRoom(room, cmd) },
		BroadcastExcept:     func(cmd protocol.ServerCommand, exclude map[int]struct{}) { h.BroadcastRoomExcept(room, cmd, exclude) },
		BroadcastToMonitors: func(cmd protocol.ServerCommand) { h.broadcastToMonitors(room, cmd) },
		PickNextHostID:      pickNextHost,
		Lang:                h.State.ServerLang,
		Logger:              h.State.Logger,
		DisbandRoom:         h.DisbandRoom,
		OnEnterPlaying:      h.OnEnterPlaying,
		OnGameEnd:           h.OnGameEnd,
		WSService:           h.State.WSService,
		SystemChatUserID:    h.State.SystemChatUserID,
	}
	room.lifecycle.Store(lc)
	return lc
}

// clientRoomStateForJoin 构造「加入房间时」客户端可见的房间状态：
//   - 默认直接返回房间当前状态；
//   - ProtocolHack：若非 SelectChart 但已有谱面，伪装成 SelectChart 让客户端先获知谱面 ID。
//
// 两处共用（ProcessJoinRoom、session.handleAuthenticate 的 WaitForReady 重连），
// 集中避免行为漂移。调用方须持 room.Mu。
func (r *Room) clientRoomStateForJoin() protocol.RoomState {
	st := r.ClientRoomState()
	if _, isSelect := r.State.(StateSelectChart); !isSelect && r.Chart != nil {
		cid := int32(r.Chart.ID)
		st = protocol.RoomStateSelectChart{ID: &cid}
	}
	return st
}

// ClientRoomStateForJoin 是 clientRoomStateForJoin 的可导出包装，供 network 包等
// 跨包调用方使用。语义与调用条件保持一致（调用方须持 room.Mu）。
func (h *Hub) ClientRoomStateForJoin(room *Room) protocol.RoomState {
	return room.clientRoomStateForJoin()
}

// RequireRoom 返回用户所在房间，不在任何房间则返回 errNoRoom。
func (h *Hub) RequireRoom(user *User) (*Room, error) {
	if user.Room == nil {
		return nil, errNoRoom
	}
	return user.Room, nil
}

// CheckRoomAllReady 推进房间状态机并返回是否需解散房间（比赛 AutoDisband）。
// 调用方须持 state.Mu；返回 disband=true 时调用方应在 room.Mu 释放后调 DisbandRoom。
func (h *Hub) CheckRoomAllReady(room *Room) bool {
	return room.CheckAllReady(h.MakeRoomLifecycle(room))
}

func (h *Hub) isBanned(user *User) bool {
	_, banned := h.State.BannedUsers[user.ID]
	return banned
}