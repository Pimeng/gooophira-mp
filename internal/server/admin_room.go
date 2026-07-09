package server

import (
	"errors"
	"strconv"

	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

// 管理员房间操作（供 CLI lock/cycle/sethost 命令调用）。与 contest.go 的 Hub 方法一致：
// 调用方无须持任何锁；方法内部自行获取 room.Mu。广播在 room.Mu 内调用安全——
// BroadcastRoomMessage 经 ParticipantsSnapshot(atomic 无锁读) + TrySendFrameOwned(非阻塞)，
// 不重入 room.Mu、不阻塞 I/O。

// ErrAlreadyHost 表示目标用户已是当前房主，无需转移。
var ErrAlreadyHost = errors.New("user-already-host")

// adminActorLabel 返回管理员操作在房间日志中的身份标识（本地化）。
func adminActorLabel(lang *l10n.Language) string {
	return l10n.TL(lang, "cli-admin-actor", nil)
}

// SetRoomLocked 管理员强制锁定/解锁房间，广播 MsgLockRoom 同步客户端。
// 与客户端 CmdLockRoom（dispatch.go）的区别：绕过房主校验，由管理员直接干预。
func (h *Hub) SetRoomLocked(room *Room, lock bool) {
	lc := h.MakeRoomLifecycle(room)
	room.Mu.Lock()
	defer room.Mu.Unlock()
	room.Locked = lock
	room.MarkAndBroadcast(lc, "log-room-lock", map[string]string{
		"user": adminActorLabel(lc.Lang),
		"lock": strconv.FormatBool(lock),
	}, protocol.MsgLockRoom{Lock: lock})
}

// SetRoomCycle 管理员开关循环模式，广播 MsgCycleRoom。
func (h *Hub) SetRoomCycle(room *Room, cycle bool) {
	lc := h.MakeRoomLifecycle(room)
	room.Mu.Lock()
	defer room.Mu.Unlock()
	room.Cycle = cycle
	room.MarkAndBroadcast(lc, "log-room-cycle", map[string]string{
		"user":  adminActorLabel(lc.Lang),
		"cycle": strconv.FormatBool(cycle),
	}, protocol.MsgCycleRoom{Cycle: cycle})
}

// TransferHost 即时转移房主（不限 cycle 模式）。目标须在房内且非当前房主，否则返回 error。
// 广播 MsgNewHost；给旧/新房主各发 SrvChangeHost。清除 nextHostID 防与 nexthost 预约冲突。
// 房主转移模式参照 rotateCycleHost（roomlogic.go），但由管理员显式指定而非按 ID 升序轮换。
func (h *Hub) TransferHost(room *Room, userID int) error {
	lc := h.MakeRoomLifecycle(room)
	room.Mu.Lock()
	defer room.Mu.Unlock()
	if !room.ContainsUser(userID) {
		return ErrUserNotInRoom
	}
	if room.HostID == userID {
		return ErrAlreadyHost
	}
	old := room.HostID
	room.HostID = userID
	room.ClearNextHost()
	room.LogAndBroadcast(lc, "log-room-host-changed-admin", map[string]string{
		"old":  strconv.Itoa(old),
		"next": strconv.Itoa(userID),
	}, protocol.MsgNewHost{User: int32FromInt(userID)})
	// 经 room.usersMap（持 room.Mu 安全）取 *User 指针，避免读 state.Users 引入 data race。
	if u := room.usersMap[old]; u != nil {
		u.TrySend(protocol.SrvChangeHost{IsHost: false})
	}
	if u := room.usersMap[userID]; u != nil {
		u.TrySend(protocol.SrvChangeHost{IsHost: true})
	}
	return nil
}
