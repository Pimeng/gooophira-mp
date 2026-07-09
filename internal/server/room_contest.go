package server

// 比赛模式管理（供 HTTP admin 路由与 CLI contest 命令共用）。所有方法假定调用方已持 state.Mu；
// 涉及房间字段的方法会自行获取 room.Mu。
// 比赛房间的入房白名单校验、手动开赛、结束自动解散等「执行」逻辑在 roomlogic.go。

// contestWhitelist 由给定 userIDs（为空则取当前参与者）构建白名单，并始终并入当前参与者，
// 避免把已在房内的人挡在白名单外。
func contestWhitelist(room *Room, userIDs []int) map[int]struct{} {
	set := make(map[int]struct{})
	if len(userIDs) > 0 {
		for _, id := range userIDs {
			set[id] = struct{}{}
		}
	} else {
		for _, id := range room.AllParticipantIDs() {
			set[id] = struct{}{}
		}
	}
	for _, id := range room.AllParticipantIDs() {
		set[id] = struct{}{}
	}
	return set
}

// EnableContest 把房间设为比赛模式（手动开赛 + 结束自动解散）。userIDs 为空则用当前参与者作白名单。
func (h *Hub) EnableContest(room *Room, userIDs []int) {
	room.Mu.Lock()
	defer room.Mu.Unlock()
	room.Contest = &Contest{Whitelist: contestWhitelist(room, userIDs), ManualStart: true, AutoDisband: true}
}

// DisableContest 取消房间的比赛模式。
func (h *Hub) DisableContest(room *Room) {
	room.Mu.Lock()
	defer room.Mu.Unlock()
	room.Contest = nil
}

// SetContestWhitelist 替换比赛房间白名单（始终并入当前参与者）。房间非比赛模式时返回 false。
func (h *Hub) SetContestWhitelist(room *Room, userIDs []int) bool {
	room.Mu.Lock()
	defer room.Mu.Unlock()
	if room.Contest == nil {
		return false
	}
	room.Contest.Whitelist = contestWhitelist(room, userIDs)
	return true
}

// StartContest 强制开赛一个比赛房间：须处于 WaitForReady 且已选谱；force=false 时要求全员就绪。
// 返回 nil 表示已开赛；否则返回对应错误（contest-room-not-found / room-not-waiting /
// no-chart-selected / not-all-ready）。
func (h *Hub) StartContest(room *Room, force bool) error {
	room.Mu.Lock()
	defer room.Mu.Unlock()
	if room.Contest == nil {
		return errContestNotFound
	}
	st, ok := room.State.(StateWaitForReady)
	if !ok {
		return errRoomNotWaiting
	}
	if room.Chart == nil {
		return errNoChartSelected
	}
	if !force {
		for _, id := range room.AllParticipantIDs() {
			if _, ready := st.Started[id]; !ready {
				return errNotAllReady
			}
		}
	}
	room.startPlaying(h.MakeRoomLifecycle(room), nil)
	return nil
}
