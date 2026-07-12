package server

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

// InternalRoomState 是房间内部状态（比客户端可见状态更详细）。tagged union。
type InternalRoomState interface{ isInternalRoomState() }

type (
	// StateSelectChart 选谱阶段。
	StateSelectChart struct{}
	// StateWaitForReady 等待就绪阶段；Started 是已就绪玩家 id 集合。
	StateWaitForReady struct {
		Started map[int]struct{}
	}
	// StatePlaying 游戏进行中。
	StatePlaying struct {
		// Results 是各玩家提交的成绩。
		Results map[int]config.RecordData
		// Aborted 是已中止的玩家 id。
		Aborted map[int]struct{}
		// ReconnectNotified 是已播报过「等待重连」的挂起玩家 id（避免重复刷屏）。
		ReconnectNotified map[int]struct{}
		// StartedAt 进入 Playing 状态的墙钟时间（用于结算时计算本局时长）。
		StartedAt time.Time
	}
)

func (StateSelectChart) isInternalRoomState()  {}
func (StateWaitForReady) isInternalRoomState() {}
func (StatePlaying) isInternalRoomState()      {}

// Contest 是比赛模式配置。
type Contest struct {
	Whitelist   map[int]struct{}
	ManualStart bool
	AutoDisband bool
}

// RoomLog 是一条房间日志。
type RoomLog struct {
	Message   string
	Timestamp int64
}

// 房间相关常量。
const (
	maxRecentLogs     = 50
	maxLogMessageLen  = 1000
	roomLogTrailEllip = "..."
)

// ErrOnlyHost 表示操作要求房主权限但调用者不是房主。
var ErrOnlyHost = errors.New("room-only-host")

// Room 代表一个多人游戏房间，管理状态机、成员、房主转移、结果统计与房间日志。
type Room struct {
	// Mu 分段锁：保护房间内部状态。Touches/Judges 热路径仅持此锁，
	// 不竞争全局 state.Mu，不同房间之间完全并行。
	Mu sync.Mutex
	// ID 房间唯一标识。
	ID protocol.RoomID
	// MaxUsers 最大用户数。
	MaxUsers int
	// ReplayEligible 是否允许录制回放。
	ReplayEligible bool
	// HostID 当前房主用户 ID。
	HostID int
	// State 房间内部状态（初始为选谱）。
	State InternalRoomState

	// Live 直播模式。
	Live bool
	// Locked 已锁定（禁止加入）。
	Locked bool
	// Cycle 循环模式（结束后轮换房主）。
	Cycle bool
	// Contest 比赛配置（nil = 普通房间）。
	Contest *Contest

	// users/monitors 保持加入顺序（cycle 房主轮换依赖此顺序），成员数 ≤64，线性查找足够。
	users    []int
	monitors []int

	// usersMap/monitorUsers 是 user 指针缓存，供 participantsSnapshot 刷新使用，
	// 避免广播路径访问全局 state.Users 引入额外锁竞争。
	usersMap     map[int]*User
	monitorUsers map[int]*User

	// participantsSnapshot 是所有参与者（玩家+观战者）的 *User 指针快照，
	// 每次 users/monitors 变更时在 room.Mu 内刷新。BroadcastRoom 无锁读取，
	// 消除 AllParticipantIDs() 的 []int 分配 + state.Users map 查找。
	participantsSnapshot atomic.Pointer[[]*User]

	// Chart 当前选中的谱面（nil = 未选）。
	Chart *config.Chart

	// readyCancel 取消「准备倒计时」（房主下发游戏开始后 60 秒强制开赛）。
	// nil 表示无活跃倒计时。用 atomic.Pointer 访问，无需持有 Mu。
	readyCancel atomic.Pointer[context.CancelFunc]

	// playDeadlineCancel 取消「结算超时」：自首位玩家提交成绩起 120 秒强制结束本局，
	// 将未结算玩家标记为 Aborted。nil 表示无活跃倒计时。用 atomic.Pointer 访问，无需持有 Mu。
	playDeadlineCancel atomic.Pointer[context.CancelFunc]

	// nextHostID 是管理员通过 CLI 指定的「下一轮房主」候选 ID，仅 cycle 模式下
	// rotateCycleHost 会消费。nil 表示未指定；一次性使用后清空。访问须持 room.Mu。
	nextHostID *int

	// logMu 专门保护 recentLogs。AddLog/GetRecentLogs 的调用路径既包含
	// 已持 room.Mu 的（OnUserLeave），也包含只持 state.Mu 的（checkPlaying），
	// 还包含 ProtocolHack 延迟回调；用 room.Mu 会自死锁，用 state.Mu 又
	// 与测试路径不兼容。logMu 是叶级锁，仅围绕切片操作短临界区，零嵌套风险。
	logMu      sync.Mutex
	recentLogs []RoomLog

	// lifecycle 缓存 MakeRoomLifecycle 的结果。RoomLifecycle 捕获 h 和 room，
	// 两者在房间生命周期内不变，故只需创建一次。用 atomic.Pointer 支持无锁读取。
	lifecycle atomic.Pointer[RoomLifecycle]
}

// NewRoom 创建新房间，房主默认加入用户列表。
func NewRoom(id protocol.RoomID, hostID, maxUsers int, replayEligible bool) *Room {
	return &Room{
		ID:             id,
		MaxUsers:       maxUsers,
		ReplayEligible: replayEligible,
		HostID:         hostID,
		State:          StateSelectChart{},
		users:          []int{hostID},
		usersMap:       make(map[int]*User),
		monitorUsers:   make(map[int]*User),
	}
}

// AddLog 追加一条房间日志（超长截断），维持最多 maxRecentLogs 条。
// 线程安全：通过 logMu 自保护，调用方无需持任何锁。
func (r *Room) AddLog(message string, timestamp int64) {
	if len(message) > maxLogMessageLen {
		message = message[:maxLogMessageLen] + roomLogTrailEllip
	}
	r.logMu.Lock()
	r.recentLogs = append(r.recentLogs, RoomLog{Message: message, Timestamp: timestamp})
	if len(r.recentLogs) > maxRecentLogs {
		r.recentLogs = r.recentLogs[len(r.recentLogs)-maxRecentLogs:]
	}
	r.logMu.Unlock()
}

// GetRecentLogs 返回最近房间日志的副本。线程安全：通过 logMu 自保护。
func (r *Room) GetRecentLogs() []RoomLog {
	r.logMu.Lock()
	defer r.logMu.Unlock()
	return append([]RoomLog(nil), r.recentLogs...)
}

// IsLive 报告是否直播模式。
func (r *Room) IsLive() bool { return r.Live }

// IsLocked 报告是否已锁定。
func (r *Room) IsLocked() bool { return r.Locked }

// IsCycle 报告是否循环模式。
func (r *Room) IsCycle() bool { return r.Cycle }

// RefreshLive 依据「有观战者」或「应录制回放」重算并写回房间 live 状态，返回新值。
// 对应 TS 的 roomUtils.refreshRoomLive。
func (r *Room) RefreshLive(replayEnabled bool) bool {
	r.Live = len(r.monitors) > 0 || (replayEnabled && r.ReplayEligible)
	return r.Live
}

// CheckHost 校验 user 是否房主，不是则返回 ErrOnlyHost。
func (r *Room) CheckHost(user *User) error {
	if r.HostID != user.ID {
		return ErrOnlyHost
	}
	return nil
}

// IsHost 报告 user 是否为房间房主（无锁版——只读快照，适用于协议补偿等非关键路径）。
func (r *Room) IsHost(user *User) bool {
	return user != nil && r.HostID == user.ID
}

// UserIDs 返回普通玩家 id（加入顺序，副本）。
func (r *Room) UserIDs() []int { return append([]int(nil), r.users...) }

// UsersMap 返回房间内普通玩家的 user 指针映射（内部 map 直接引用，不拷贝）。
// 调用方须持 room.Mu。
func (r *Room) UsersMap() map[int]*User { return r.usersMap }

// PlayingState 返回 StatePlaying（若当前为 Playing 状态）及 true；否则返回零值与 false。
// 用于结算路径从 Room 安全读取成绩数据。
func (r *Room) PlayingState() (StatePlaying, bool) {
	st, ok := r.State.(StatePlaying)
	return st, ok
}

// MonitorIDs 返回观战者 id（加入顺序，副本）。
func (r *Room) MonitorIDs() []int { return append([]int(nil), r.monitors...) }

// MonitorUsers 返回当前观战者 user 指针列表（副本），供 touches/judges 热路径广播使用。
func (r *Room) MonitorUsers() []*User {
	out := make([]*User, 0, len(r.monitorUsers))
	for _, u := range r.monitorUsers {
		out = append(out, u)
	}
	return out
}

// AllParticipantIDs 返回所有参与者 id（玩家 + 观战者，副本）。
func (r *Room) AllParticipantIDs() []int {
	out := make([]int, 0, len(r.users)+len(r.monitors))
	out = append(out, r.users...)
	out = append(out, r.monitors...)
	return out
}

// UserCount 返回普通玩家数。
func (r *Room) UserCount() int { return len(r.users) }

// MonitorCount 返回观战者数。
func (r *Room) MonitorCount() int { return len(r.monitors) }

// IsEmpty 报告房间是否无任何成员。
func (r *Room) IsEmpty() bool { return len(r.users) == 0 && len(r.monitors) == 0 }

// ContainsUser 报告给定用户 ID 是否为房间内的普通玩家（不含观战者）。调用方须持 room.Mu。
func (r *Room) ContainsUser(id int) bool {
	for _, uid := range r.users {
		if uid == id {
			return true
		}
	}
	return false
}

// SetNextHost 指定下一轮房主候选 ID（仅 cycle 模式下 rotateCycleHost 会消费）。
// 调用方须持 room.Mu。
func (r *Room) SetNextHost(id int) { r.nextHostID = &id }

// NextHostID 返回已设置的下一轮房主候选 ID 及是否设置。调用方须持 room.Mu。
func (r *Room) NextHostID() (int, bool) {
	if r.nextHostID == nil {
		return 0, false
	}
	return *r.nextHostID, true
}

// ClearNextHost 清除下一轮房主候选。调用方须持 room.Mu。
func (r *Room) ClearNextHost() { r.nextHostID = nil }

// ClientRoomState 返回当前面向客户端的房间状态。
func (r *Room) ClientRoomState() protocol.RoomState {
	switch r.State.(type) {
	case StateWaitForReady:
		return protocol.RoomStateWaitingForReady{}
	case StatePlaying:
		return protocol.RoomStatePlaying{}
	default: // StateSelectChart
		var id *int32
		if r.Chart != nil {
			v := int32(r.Chart.ID)
			id = &v
		}
		return protocol.RoomStateSelectChart{ID: id}
	}
}
