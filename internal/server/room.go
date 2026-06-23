package server

import (
	"errors"

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

	// Chart 当前选中的谱面（nil = 未选）。
	Chart *config.Chart

	recentLogs []RoomLog
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
	}
}

// AddLog 追加一条房间日志（超长截断），维持最多 maxRecentLogs 条。
func (r *Room) AddLog(message string, timestamp int64) {
	if len(message) > maxLogMessageLen {
		message = message[:maxLogMessageLen] + roomLogTrailEllip
	}
	r.recentLogs = append(r.recentLogs, RoomLog{Message: message, Timestamp: timestamp})
	if len(r.recentLogs) > maxRecentLogs {
		r.recentLogs = r.recentLogs[len(r.recentLogs)-maxRecentLogs:]
	}
}

// GetRecentLogs 返回最近房间日志的副本。
func (r *Room) GetRecentLogs() []RoomLog {
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

// UserIDs 返回普通玩家 id（加入顺序，副本）。
func (r *Room) UserIDs() []int { return append([]int(nil), r.users...) }

// MonitorIDs 返回观战者 id（加入顺序，副本）。
func (r *Room) MonitorIDs() []int { return append([]int(nil), r.monitors...) }

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
