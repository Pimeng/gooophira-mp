package server

import (
	"sync"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/common/protocol"
)

// 观战数据聚合缓冲：高频实时数据（Touches/Judges）按 ~50ms 窗口合并后批量转发给观战者，
// 避免高频帧直接冲击网络。对应 TS network/session/monitorBuffer.ts。
//
// 动态 flush 间隔：缓冲越大刷得越急（降低延迟峰值）。
const (
	monitorMaxBufferSlow = 50
	monitorMaxBufferFast = 200
	monitorFlushSlow     = 50 * time.Millisecond
	monitorFlushFast     = 20 * time.Millisecond
	monitorFlushUrgent   = 10 * time.Millisecond
)

type monitorTouchItem struct {
	room   *Room
	player int
	frames []protocol.TouchFrame
}

type monitorJudgeItem struct {
	room   *Room
	player int
	judges []protocol.JudgeEvent
}

// AggregatingMonitorBuffer 实现 MonitorBuffer：把同一玩家在窗口内的多帧合并成一条命令再广播。
//
// 并发：BufferTouches/BufferJudges 在 room.Mu 下被快速入队（持本缓冲自身的 mu），不访问全局 state。
// flush 在定时器 goroutine 上先取走缓冲（持 b.mu，随即释放），再对每个 room 持 room.Mu 读取当前
// 观战者并 TrySend。b.mu 与 room.Mu 不会被同一路径同时持有，故无环路死锁。
type AggregatingMonitorBuffer struct {
	mu     sync.Mutex
	touch  []monitorTouchItem
	judge  []monitorJudgeItem
	timer  *time.Timer
	closed bool
}

// NewMonitorBuffer 创建观战数据聚合缓冲。
func NewMonitorBuffer() *AggregatingMonitorBuffer {
	return &AggregatingMonitorBuffer{}
}

// 确保实现 MonitorBuffer。
var _ MonitorBuffer = (*AggregatingMonitorBuffer)(nil)

// BufferTouches 入队一批触摸帧（调用方持 room.Mu）。
func (b *AggregatingMonitorBuffer) BufferTouches(room *Room, userID int, frames []protocol.TouchFrame) {
	if len(frames) == 0 {
		return
	}
	b.mu.Lock()
	b.touch = append(b.touch, monitorTouchItem{room: room, player: userID, frames: frames})
	b.scheduleLocked()
	b.mu.Unlock()
}

// BufferJudges 入队一批判定事件（调用方持 room.Mu）。
func (b *AggregatingMonitorBuffer) BufferJudges(room *Room, userID int, judges []protocol.JudgeEvent) {
	if len(judges) == 0 {
		return
	}
	b.mu.Lock()
	b.judge = append(b.judge, monitorJudgeItem{room: room, player: userID, judges: judges})
	b.scheduleLocked()
	b.mu.Unlock()
}

// scheduleLocked 按当前缓冲规模安排一次延迟 flush（调用方持 b.mu）。
func (b *AggregatingMonitorBuffer) scheduleLocked() {
	if b.timer != nil || b.closed {
		return
	}
	interval := monitorFlushSlow
	switch size := len(b.touch) + len(b.judge); {
	case size > monitorMaxBufferFast:
		interval = monitorFlushUrgent
	case size > monitorMaxBufferSlow:
		interval = monitorFlushFast
	}
	b.timer = time.AfterFunc(interval, b.Flush)
}

// Flush 立即合并并广播缓冲（合并后每玩家一条命令）。定时器与手动/关闭路径共用。
func (b *AggregatingMonitorBuffer) Flush() {
	b.mu.Lock()
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	touches, judges := b.touch, b.judge
	b.touch, b.judge = nil, nil
	b.mu.Unlock()
	if len(touches) == 0 && len(judges) == 0 {
		return
	}

	b.broadcastTouches(touches)
	b.broadcastJudges(judges)
}

// Stop 停止后台刷写并做最后一次 flush（关闭时调用，幂等）。
func (b *AggregatingMonitorBuffer) Stop() {
	b.mu.Lock()
	b.closed = true
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	b.mu.Unlock()
	b.Flush()
}

// broadcastTouches 按 (房间, 玩家) 合并帧后广播给各房间当前观战者（调用方不持任何锁）。
func (b *AggregatingMonitorBuffer) broadcastTouches(items []monitorTouchItem) {
	merged := make(map[*Room]map[int][]protocol.TouchFrame, 8)
	var order []*Room
	for _, it := range items {
		pm := merged[it.room]
		if pm == nil {
			pm = make(map[int][]protocol.TouchFrame, len(items)/2+1)
			merged[it.room] = pm
			order = append(order, it.room)
		}
		pm[it.player] = append(pm[it.player], it.frames...)
	}
	for _, room := range order {
		room.Mu.Lock()
		users := room.MonitorUsers()
		room.Mu.Unlock()
		if len(users) == 0 {
			continue
		}
		for player, frames := range merged[room] {
			cmd := protocol.SrvTouches{Player: int32(player), Frames: frames}
			// 预编码一次帧，广播给所有观战者
			frame := encodeServerCommandFrame(cmd)
			if frame == nil {
				continue
			}
			for _, u := range users {
				u.TrySendFrame(frame)
			}
		}
	}
}

// broadcastJudges 按 (房间, 玩家) 合并判定事件后广播给各房间当前观战者（调用方不持任何锁）。
func (b *AggregatingMonitorBuffer) broadcastJudges(items []monitorJudgeItem) {
	merged := make(map[*Room]map[int][]protocol.JudgeEvent, 8)
	var order []*Room
	for _, it := range items {
		pm := merged[it.room]
		if pm == nil {
			pm = make(map[int][]protocol.JudgeEvent, len(items)/2+1)
			merged[it.room] = pm
			order = append(order, it.room)
		}
		pm[it.player] = append(pm[it.player], it.judges...)
	}
	for _, room := range order {
		room.Mu.Lock()
		users := room.MonitorUsers()
		room.Mu.Unlock()
		if len(users) == 0 {
			continue
		}
		for player, judges := range merged[room] {
			cmd := protocol.SrvJudges{Player: int32(player), Judges: judges}
			frame := encodeServerCommandFrame(cmd)
			if frame == nil {
				continue
			}
			for _, u := range users {
				u.TrySendFrame(frame)
			}
		}
	}
}
