package network

import (
	"sync"
	"time"
)

// connWindow 是单个 IP 在当前时间窗内的连接计数。
type connWindow struct {
	count   int
	resetAt time.Time
}

// connectionRateLimiter 按 IP 用滑动窗口限制 TCP 连接建立速率，超限即临时封禁，
// 防止连接洪水耗尽资源。对应 TS utils/connectionRateLimiter.ts。
//
// 注意：限流以 TCP 对端地址为键，而非 PROXY protocol 解出的真实 IP——因为 PROXY 头由
// 客户端发送、可伪造，若以其为键会被轻易绕过；真正消耗 socket 的是 TCP 对端。
type connectionRateLimiter struct {
	mu        sync.Mutex
	windows   map[string]connWindow
	banned    map[string]time.Time
	maxConns  int
	windowDur time.Duration
	banDur    time.Duration
}

func newConnectionRateLimiter(maxConns int, window, ban time.Duration) *connectionRateLimiter {
	return &connectionRateLimiter{
		windows:   make(map[string]connWindow),
		banned:    make(map[string]time.Time),
		maxConns:  maxConns,
		windowDur: window,
		banDur:    ban,
	}
}

// setMaxConns 运行时更新每窗口最大连接数（配置热重载用）。仅影响后续判定，不重置已有窗口/封禁。
func (l *connectionRateLimiter) setMaxConns(n int) {
	l.mu.Lock()
	l.maxConns = n
	l.mu.Unlock()
}

// allow 检查来自 ip 的新连接是否放行：超过窗口上限则封禁并拒绝。now 注入便于测试。
func (l *connectionRateLimiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if until, ok := l.banned[ip]; ok {
		if now.Before(until) {
			return false
		}
		delete(l.banned, ip)
	}

	w, ok := l.windows[ip]
	if !ok || !now.Before(w.resetAt) { // 无窗口或窗口已过期 → 开新窗口
		l.windows[ip] = connWindow{count: 1, resetAt: now.Add(l.windowDur)}
		return true
	}
	w.count++
	if w.count > l.maxConns {
		l.banned[ip] = now.Add(l.banDur)
		delete(l.windows, ip)
		return false
	}
	l.windows[ip] = w
	return true
}

// cleanup 清除已过期的窗口与封禁项，避免长跑下 map 随不同 IP 无限增长。
func (l *connectionRateLimiter) cleanup(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for ip, w := range l.windows {
		if !now.Before(w.resetAt) {
			delete(l.windows, ip)
		}
	}
	for ip, until := range l.banned {
		if !now.Before(until) {
			delete(l.banned, ip)
		}
	}
}
