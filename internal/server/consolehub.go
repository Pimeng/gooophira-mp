package server

import (
	"sync"
	"time"
)

// consoleLogCap 是控制台日志环形缓冲容量。
const consoleLogCap = 500

// ConsoleLogLine 是一条供 GUI 控制台展示的日志。JSON 字段名与 GUI 契约一致
// （level/message/timestamp），对齐 TS utils/consoleHub.ts ConsoleLine。
type ConsoleLogLine struct {
	Level     string `json:"level"`
	Message   string `json:"message"`
	Timestamp int64  `json:"timestamp"` // Unix 毫秒
}

// ConsoleHub 缓存最近日志（环形缓冲）供 GUI 回填/轮询，并支持订阅以实时推送新行
// （WebSocket console 频道在其上叠加）。
type ConsoleHub struct {
	mu        sync.Mutex
	buf       []ConsoleLogLine
	subs      map[int]func(ConsoleLogLine)
	nextSubID int
}

// NewConsoleHub 创建一个 ConsoleHub。
func NewConsoleHub() *ConsoleHub {
	return &ConsoleHub{}
}

// Push 追加一条日志（超出容量则丢弃最旧）并通知订阅者。由日志器的 OnLog 钩子调用，须并发安全。
// 订阅者回调在释放内部锁后调用，避免回调再次进入 hub 造成死锁。
func (h *ConsoleHub) Push(level, message string) {
	line := ConsoleLogLine{Level: level, Message: message, Timestamp: time.Now().UnixMilli()}
	h.mu.Lock()
	h.buf = append(h.buf, line)
	if len(h.buf) > consoleLogCap {
		h.buf = h.buf[len(h.buf)-consoleLogCap:]
	}
	var subs []func(ConsoleLogLine)
	if len(h.subs) > 0 {
		subs = make([]func(ConsoleLogLine), 0, len(h.subs))
		for _, fn := range h.subs {
			subs = append(subs, fn)
		}
	}
	h.mu.Unlock()
	for _, fn := range subs {
		fn(line)
	}
}

// GetRecent 返回最近 limit 条日志（limit<=0 或超过现有条数则返回全部）。
func (h *ConsoleHub) GetRecent(limit int) []ConsoleLogLine {
	h.mu.Lock()
	defer h.mu.Unlock()
	if limit <= 0 || limit > len(h.buf) {
		limit = len(h.buf)
	}
	out := make([]ConsoleLogLine, limit)
	copy(out, h.buf[len(h.buf)-limit:])
	return out
}

// Subscribe 注册一个新日志行回调，返回取消订阅函数。供 WebSocket console 频道实时推送。
func (h *ConsoleHub) Subscribe(fn func(ConsoleLogLine)) func() {
	h.mu.Lock()
	if h.subs == nil {
		h.subs = make(map[int]func(ConsoleLogLine))
	}
	id := h.nextSubID
	h.nextSubID++
	h.subs[id] = fn
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		delete(h.subs, id)
		h.mu.Unlock()
	}
}
