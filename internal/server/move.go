package server

// MoveUser 把一名「已离线」的用户从当前房间迁移到另一个空闲房间（管理员操作）。
// 对应 TS adminUserRoutes 的 /admin/users/:id/move。调用方须持 state.Mu。
//
// 约束：用户须已断线（避免与其客户端状态冲突）、当前在某房间、源与目标房间均处于 SelectChart，
// 目标房间通过入房校验且未满。成功后用户被加入目标房间、退出源房间（源空则解散），并更新其
// monitor 标记与所属房间。
func (h *Hub) MoveUser(user *User, toRoom *Room, monitor bool) error {
	if user.Session != nil {
		return errUserMustDisconnect
	}
	from := user.Room
	if from == nil {
		return errUserNotInRoom
	}
	if _, ok := from.State.(StateSelectChart); !ok {
		return errCannotMovePlaying
	}
	if _, ok := toRoom.State.(StateSelectChart); !ok {
		return errTargetRoomNotIdle
	}
	if err := toRoom.ValidateJoin(user, monitor); err != nil {
		return err
	}
	if !toRoom.AddUser(user, monitor) {
		return errJoinRoomFull
	}
	// 先入目标，再退源（与 TS 顺序一致）；源房间空则解散。
	// 源房间已校验为 SelectChart，OnUserLeave 不会进 checkPlaying，故 disband 恒 false。
	// 但仍须持 from.Mu（OnUserLeave 假设调用方持锁，原代码遗漏，此处补上）。
	from.Mu.Lock()
	shouldDrop, _ := from.OnUserLeave(h.MakeRoomLifecycle(from), user)
	from.Mu.Unlock()
	if shouldDrop {
		delete(h.State.Rooms, from.ID)
	}
	user.Monitor = monitor
	user.Room = toRoom
	return nil
}
