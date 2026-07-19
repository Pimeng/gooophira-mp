package server

import (
	"context"
	"errors"
	"time"
)

// Hub 把 ServerState 与上游依赖（Phira API、回放、观战缓冲、状态钩子）组合为命令派发
// 的编排层。所有房间操作都经 Session 接口（User.TrySend）发送，因而无需依赖 network 包，
// 可用 mock 完整单测。
//
// 并发模型：TS 靠单线程事件循环串行化所有命令。Go 中由 network 层在调用
// ProcessClientCommand 前后持有全局 ServerState.Mu，把命令处理整体串行化（等价于 TS 事件
// 循环）。因此 Hub 方法内部**不再加锁**（调用方已持锁）；广播只向各会话的发送通道入队
// （非阻塞），真正的 socket 写入在各自的写入协程中于锁外完成，避免持锁阻塞 I/O。
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

func (h *Hub) isBanned(user *User) bool {
	_, banned := h.State.BannedUsers[user.ID]
	return banned
}
