package server

import (
	"context"
	"strconv"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

// playDeadlineDuration 是「结算超时」总时长：自首位玩家提交成绩起 120 秒后强制结束本局，
// 将未结算玩家标记为 Aborted，避免长时间挂起影响其他玩家。用 var 而非 const，便于测试覆盖为小值。
var playDeadlineDuration = 120 * time.Second

// cancelPlayDeadline 取消房间的「结算超时」倒计时。无活跃倒计时时空操作。
// 用 CAS 保证只 cancel 一次，可在任意锁状态下调用。
func (r *Room) cancelPlayDeadline() {
	for {
		p := r.playDeadlineCancel.Load()
		if p == nil {
			return
		}
		if r.playDeadlineCancel.CompareAndSwap(p, nil) {
			(*p)()
			return
		}
	}
}

// startPlayDeadline 启动「结算超时」：自首位玩家提交成绩起 120 秒后强制结束本局。
// 到点后将未结算玩家标记为 Aborted 并广播 MsgAbort，发送系统聊天通知，
// 然后调 CheckAllReady 触发正常结算流程（排名/GameEnd/回到 SelectChart）。
// 比赛房不启动此倒计时（由管理员手动管理）。调用方必须持有 state.Mu。
func (h *Hub) startPlayDeadline(room *Room) {
	if room.Contest != nil {
		return
	}
	room.cancelPlayDeadline()
	ctx, cancel := context.WithCancel(context.Background())
	room.playDeadlineCancel.Store(&cancel)

	state := h.State
	roomID := room.ID
	lang := state.ServerLang
	sysID := state.SystemChatUserID()
	seconds := strconv.Itoa(int(playDeadlineDuration / time.Second))

	time.AfterFunc(playDeadlineDuration, func() {
		if ctx.Err() != nil {
			return
		}
		state.Mu.Lock()
		if state.Rooms[roomID] != room {
			state.Mu.Unlock()
			return
		}
		room.Mu.Lock()
		st, ok := room.State.(StatePlaying)
		if !ok {
			room.Mu.Unlock()
			state.Mu.Unlock()
			return
		}
		lc := h.MakeRoomLifecycle(room)
		// 将所有未结算玩家标记为 Aborted 并广播 MsgAbort，使其不再阻塞结算。
		for _, id := range room.users {
			if _, hasResult := st.Results[id]; hasResult {
				continue
			}
			if _, hasAbort := st.Aborted[id]; hasAbort {
				continue
			}
			st.Aborted[id] = struct{}{}
			name := strconv.Itoa(id)
			if u := state.Users[id]; u != nil {
				name = u.Name
			}
			room.logRoomMark(lc, "log-room-abort", map[string]string{"user": name})
			h.BroadcastRoomMessage(room, protocol.MsgAbort{User: int32FromInt(id)})
		}
		// 发送系统聊天通知。
		if hint, ok := tlOrSkip(lang, "chat-play-deadline", map[string]string{"seconds": seconds}); ok {
			h.BroadcastRoomMessage(room, protocol.MsgChat{User: sysID, Content: hint})
		}
		room.NotifyWebSocket(lc)
		// 触发正常结算流程（checkPlaying 会发现全员已完成 → 排名/GameEnd/SelectChart）。
		disband := room.CheckAllReady(lc)
		room.Mu.Unlock()
		if disband {
			h.DisbandRoom(room)
		}
		state.Mu.Unlock()
	})
}
