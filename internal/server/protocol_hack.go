package server

import (
	"sync/atomic"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

// ProtocolHackDelay 是「客户端协议补偿」的默认延迟。原 jphira-mp 用 Netty 的
// CompletableFuture.delayedExecutor(2, MILLISECONDS)，Go 端没有同等的 setImmediate 机制，
// 需要一个保守的延迟确保客户端已处理完上一条响应（CreateRoom/JoinRoom/Auth 等）后再发送
// 后续协议补偿消息。2ms 在 Netty 足够，但 Go 的写循环是独立 goroutine、且经过 TCP 缓冲
// 套接字——保险起见默认设 5ms，可由 -protocol-hack-delay CLI 参数覆盖（0 关闭）。可经
// atomic 改写以配合测试 / 运行时调优。
//
// 真正的「零延迟」方案需要客户端发送 ACK 消息并增加一对协议包，超出本项目当前范围。
var protocolHackDelay atomic.Int64

func init() { protocolHackDelay.Store(int64(5 * time.Millisecond)) }

// SetProtocolHackDelay 运行时设置 ProtocolHack 延迟（用于测试或热调优）。传入 0 关闭。
func SetProtocolHackDelay(d time.Duration) { protocolHackDelay.Store(int64(d)) }

// ProtocolHack 集中实现客户端协议补偿（对应 jphira-mp 的 RoomSnapshot.ProtocolHack）。
// 仅在客户端状态机已知可能与服务端不一致时调用——典型场景：
//   - 建房后服务端要追加「观战者已就位」的幻觉以触发回放录制（forceSyncInfo）；
//   - 重连后客户端房间状态需重对齐（fixClientRoomState）。
//
// 所有方法都是非阻塞的：内部通过 time.AfterFunc 延迟发送，不在持锁热路径上等待。
type ProtocolHack struct {
	hub   *Hub
	delay time.Duration
}

// NewProtocolHack 构造 ProtocolHack 辅助器。
func (h *Hub) NewProtocolHack() *ProtocolHack {
	d := time.Duration(protocolHackDelay.Load())
	return &ProtocolHack{hub: h, delay: d}
}

// schedule 在延迟后执行 fn；延迟 0 表示立即异步派发（避免栈膨胀）。
func (ph *ProtocolHack) schedule(fn func()) {
	if fn == nil {
		return
	}
	d := ph.delay
	if d <= 0 {
		go fn()
		return
	}
	time.AfterFunc(d, fn)
}

// forceSyncHost 把指定用户的房主状态对齐到房间当前 host。玩家不在房间则 no-op。
func (ph *ProtocolHack) forceSyncHost(room *Room, user *User) {
	if user == nil || room == nil {
		return
	}
	isHost := room.HostID == user.ID
	ph.schedule(func() {
		user.TrySend(protocol.SrvChangeHost{IsHost: isHost})
	})
}

// forceSyncInfo 对齐玩家对房间的完整认知：房主状态 + 观战者进出 + 房间状态。
// 对应 jphira-mp 的 forceSyncInfo：补发假观战者加入/离开，并修复客户端房间状态。
func (ph *ProtocolHack) forceSyncInfo(room *Room, user *User) {
	if user == nil || room == nil {
		return
	}
	hub := ph.hub
	lang := hub.State.ServerLang
	recorder := hub.State.ReplayRecorder
	live := room.IsLive()
	delay := ph.delay

	// 1) 立即（非延迟）发送：房主状态、假观战者加入消息
	if !room.IsHost(user) {
		user.TrySend(protocol.SrvChangeHost{IsHost: false})
	}
	if live && recorder != nil {
		name := l10n.TL(lang, "replay-recorder-name", nil)
		fake := recorder.FakeMonitorInfo(name)
		user.TrySend(protocol.SrvOnJoinRoom{Info: fake})
		user.TrySend(protocol.SrvMessage{
			Message: protocol.MsgJoinRoom{User: fake.ID, Name: fake.Name},
		})
		// 2) 延迟发送：让客户端有时间把假观战者加入其本地用户列表后再离开——这是触发
		// 「观战者已加入」回放标记的关键序列。
		ph.schedule(func() {
			user.TrySend(protocol.SrvMessage{
				Message: protocol.MsgLeaveRoom{User: fake.ID, Name: fake.Name},
			})
		})
	}

	// 3) 修复客户端房间状态（仅在需要时：非 SelectChart 但有谱面）
	if _, isSelect := room.State.(StateSelectChart); !isSelect && room.Chart != nil {
		// 二次延迟：确保上面假观战者离开消息已被处理
		time.AfterFunc(delay, func() {
			ph.fixClientRoomState0(room, user)
		})
	}
}

// fixClientRoomState 在「非 SelectChart 但有谱面」时，伪装成 SelectChart 让客户端先
// 获知谱面 ID，再切回真实状态（两步序列）。
func (ph *ProtocolHack) fixClientRoomState(room *Room, user *User) {
	if user == nil || room == nil {
		return
	}
	if _, isSelect := room.State.(StateSelectChart); isSelect || room.Chart == nil {
		return
	}
	ph.schedule(func() { ph.fixClientRoomState0(room, user) })
}

// FixClientRoomState 是 fixClientRoomState 的可导出包装，供 network 包等跨包调用。
func (ph *ProtocolHack) FixClientRoomState(room *Room, user *User) {
	ph.fixClientRoomState(room, user)
}

// fixClientRoomState0 是 fixClientRoomState 的执行体：先 SelectChart，再延迟切回真实态。
// 假定房间 Chart 字段非 nil。
func (ph *ProtocolHack) fixClientRoomState0(room *Room, user *User) {
	cid := int32(room.Chart.ID)
	user.TrySend(protocol.SrvChangeState{State: protocol.RoomStateSelectChart{ID: &cid}})
	delay := ph.delay
	time.AfterFunc(delay, func() {
		user.TrySend(protocol.SrvChangeState{State: room.ClientRoomState()})
	})
}
