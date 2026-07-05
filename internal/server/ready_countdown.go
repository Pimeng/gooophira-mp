package server

import (
	"context"
	"strconv"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

// readyCountdownDuration 是「准备倒计时」总时长：房主下发游戏开始后 60 秒强制开赛。
// 用 var 而非 const，便于测试覆盖为小值。
var readyCountdownDuration = 60 * time.Second

// readyCountdownReminders 是倒计时剩余时间提醒点（在剩余 N 时发系统聊天）。
var readyCountdownReminders = []time.Duration{10 * time.Second, 5 * time.Second, 3 * time.Second, 2 * time.Second, 1 * time.Second}

// forcedStartAbortBroadcastDelay 是强制开赛后延迟广播未准备玩家 Aborted 状态的时长。
// 让客户端先消化开赛（MsgStartPlaying + SrvChangeState）动画，避免在切换途中弹出 abort 提示。
// 用 var 而非 const，便于测试覆盖为小值。
var forcedStartAbortBroadcastDelay = 500 * time.Millisecond

// cancelReadyCountdown 取消房间的「准备倒计时」。无活跃倒计时时空操作。
// 用 CAS 保证只 cancel 一次，可在任意锁状态下调用。
func (r *Room) cancelReadyCountdown() {
	for {
		p := r.readyCancel.Load()
		if p == nil {
			return
		}
		if r.readyCancel.CompareAndSwap(p, nil) {
			(*p)()
			return
		}
	}
}

// startReadyCountdown 启动「准备倒计时」：在剩余 10/5/3/2/1 秒时发系统聊天提醒，
// 60 秒到后强制开赛（未准备玩家标记为 Aborted，本局不能参与）。比赛房不启动倒计时。
// 调用方必须持有 state.Mu。
func (h *Hub) startReadyCountdown(room *Room) {
	if room.Contest != nil && room.Contest.ManualStart {
		return
	}
	room.cancelReadyCountdown()
	ctx, cancel := context.WithCancel(context.Background())
	room.readyCancel.Store(&cancel)

	lang := h.State.ServerLang
	sysID := h.State.SystemChatUserID()
	state := h.State
	roomID := room.ID

	for _, reminder := range readyCountdownReminders {
		delay := readyCountdownDuration - reminder
		sec := int(reminder / time.Second)
		time.AfterFunc(delay, func() {
			if ctx.Err() != nil {
				return
			}
			hint, ok := tlOrSkip(lang, "chat-ready-countdown", map[string]string{"seconds": strconv.Itoa(sec)})
			if !ok {
				return
			}
			state.Mu.Lock()
			if state.Rooms[roomID] != room {
				state.Mu.Unlock()
				return
			}
			room.Mu.Lock()
			if _, ok := room.State.(StateWaitForReady); !ok {
				room.Mu.Unlock()
				state.Mu.Unlock()
				return
			}
			h.BroadcastRoomMessage(room, protocol.MsgChat{User: sysID, Content: hint})
			room.Mu.Unlock()
			state.Mu.Unlock()
		})
	}

	time.AfterFunc(readyCountdownDuration, func() {
		if ctx.Err() != nil {
			return
		}
		state.Mu.Lock()
		if state.Rooms[roomID] != room {
			state.Mu.Unlock()
			return
		}
		room.Mu.Lock()
		st, ok := room.State.(StateWaitForReady)
		if !ok {
			room.Mu.Unlock()
			state.Mu.Unlock()
			return
		}
		unready := make([]int, 0)
		for _, id := range room.UserIDs() {
			if _, ready := st.Started[id]; !ready {
				unready = append(unready, id)
			}
		}
		lc := h.MakeRoomLifecycle(room)
		room.startPlaying(lc)
		if playingSt, ok := room.State.(StatePlaying); ok && len(unready) > 0 {
			for _, id := range unready {
				playingSt.Aborted[id] = struct{}{}
			}
			room.State = playingSt
			// Aborted 状态立即标记（状态机正确性），但 MsgAbort 广播延迟下发：
			// 让客户端先消化 MsgStartPlaying + SrvChangeState 的开赛动画，避免在切换途中
			// 弹出 abort 提示。回调不依赖倒计时 ctx（已被 startPlaying 取消），改用房间状态
			// 判断是否仍需广播——若 500ms 内房间已结算/解散则跳过。
			abortIDs := append([]int(nil), unready...)
			time.AfterFunc(forcedStartAbortBroadcastDelay, func() {
				h.broadcastForcedAbort(room, roomID, abortIDs)
			})
		}
		room.Mu.Unlock()
		state.Mu.Unlock()
	})
}

// broadcastForcedAbort 在强制开赛后延迟广播未准备玩家的 Aborted 状态。
// 重新校验房间仍是同一个 *Room 且处于 Playing 态；跳过已离开 / 已交成绩 / 已自行 abort 的玩家。
func (h *Hub) broadcastForcedAbort(room *Room, roomID protocol.RoomID, abortIDs []int) {
	state := h.State
	state.Mu.Lock()
	defer state.Mu.Unlock()
	if state.Rooms[roomID] != room {
		return
	}
	room.Mu.Lock()
	defer room.Mu.Unlock()
	st, ok := room.State.(StatePlaying)
	if !ok {
		return
	}
	lc := h.MakeRoomLifecycle(room)
	for _, id := range abortIDs {
		if _, aborted := st.Aborted[id]; !aborted {
			continue
		}
		name := strconv.Itoa(id)
		if u := lc.UsersByID(id); u != nil {
			name = u.Name
		}
		room.logRoomMark(lc, "log-room-abort", map[string]string{"user": name})
		h.BroadcastRoomMessage(room, protocol.MsgAbort{User: int32FromInt(id)})
	}
	room.NotifyWebSocket(lc)
}
