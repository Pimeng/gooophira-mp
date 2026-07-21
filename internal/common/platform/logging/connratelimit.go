package logging

import (
	"sync"
	"time"
)

// 连接日志限流 / IP 黑名单：对单 IP 高频连接日志做抑制，超阈值则把该 IP 拉入黑名单（其连接日志
// 在时长内被静默丢弃），防止日志洪水。对应 TS utils/rateLimiter.ts。黑名单仅抑制日志，不阻断连接。
const (
	connLogThreshold    = 10             // 窗口内最多连接日志条数
	connLogWindow       = time.Second    // 滑动窗口
	connLogBlacklistDur = time.Hour      // 黑名单时长
	connLogStaleTTL     = 24 * time.Hour // 闲置 IP 统计回收阈值
	connLogCleanupEvery = time.Hour      // 清理周期
)

// BlacklistedIP 是黑名单中的一个 IP 及其剩余时长。
type BlacklistedIP struct {
	IP        string
	ExpiresIn time.Duration
}

type ipStat struct {
	timestamps  []int64 // 窗口内连接日志时间戳（ms）
	blacklisted int64   // 黑名单到期时间（ms，0=未封）
	lastAccess  int64
}

type connRateLimiter struct {
	mu          sync.Mutex
	ips         map[string]*ipStat
	lastCleanup int64
}

func newConnRateLimiter() *connRateLimiter {
	return &connRateLimiter{ips: make(map[string]*ipStat)}
}

// shouldLog 报告该 IP 的连接日志是否应输出。超过阈值则封禁并返回 false；黑名单期内返回 false。
func (l *connRateLimiter) shouldLog(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now().UnixMilli()
	if now-l.lastCleanup > connLogCleanupEvery.Milliseconds() {
		l.cleanupLocked(now)
	}
	st := l.ips[ip]
	if st == nil {
		st = &ipStat{}
		l.ips[ip] = st
	}
	st.lastAccess = now
	if now < st.blacklisted {
		return false
	}
	st.blacklisted = 0

	windowStart := now - connLogWindow.Milliseconds()
	kept := st.timestamps[:0]
	for _, ts := range st.timestamps {
		if ts > windowStart {
			kept = append(kept, ts)
		}
	}
	kept = append(kept, now)
	st.timestamps = kept
	if len(kept) > connLogThreshold {
		st.blacklisted = now + connLogBlacklistDur.Milliseconds()
		st.timestamps = nil
		return false
	}
	return true
}

func (l *connRateLimiter) cleanupLocked(now int64) {
	l.lastCleanup = now
	for ip, st := range l.ips {
		if now-st.lastAccess > connLogStaleTTL.Milliseconds() {
			delete(l.ips, ip)
		}
	}
}

// getBlacklisted 返回当前在黑名单内的 IP 及剩余时长。
func (l *connRateLimiter) getBlacklisted() []BlacklistedIP {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now().UnixMilli()
	var out []BlacklistedIP
	for ip, st := range l.ips {
		if now < st.blacklisted {
			out = append(out, BlacklistedIP{IP: ip, ExpiresIn: time.Duration(st.blacklisted-now) * time.Millisecond})
		}
	}
	return out
}

// remove 把某 IP 移出黑名单（解封）。
func (l *connRateLimiter) remove(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if st := l.ips[ip]; st != nil {
		st.blacklisted = 0
		st.timestamps = nil
	}
}

// clear 清空所有黑名单标记。
func (l *connRateLimiter) clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, st := range l.ips {
		st.blacklisted = 0
		st.timestamps = nil
	}
}
