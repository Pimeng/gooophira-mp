// room_validate.go 把「房间状态/加入/开赛/选谱 校验」从 room_lifecycle.go 拆出，
// 使 room_lifecycle.go 更聚焦于状态机推进与生命周期副作用。
package server

import "fmt"

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