package network

import (
	"testing"
	"time"
)

func TestConnLimit_AllowsUnderCap(t *testing.T) {
	l := newConnectionRateLimiter(3, 10*time.Second, 30*time.Second)
	now := time.Now()
	for i := range 3 {
		if !l.allow("1.2.3.4", now) {
			t.Fatalf("connection %d under cap should be allowed", i+1)
		}
	}
}

func TestConnLimit_BansOverCap(t *testing.T) {
	l := newConnectionRateLimiter(3, 10*time.Second, 30*time.Second)
	now := time.Now()
	for range 3 {
		l.allow("1.2.3.4", now)
	}
	if l.allow("1.2.3.4", now) { // 第 4 次超限 → 拒绝并封禁
		t.Fatal("4th connection over cap should be rejected")
	}
	// 封禁期内即便只来一次也被拒。
	if l.allow("1.2.3.4", now.Add(5*time.Second)) {
		t.Fatal("connection during ban window should be rejected")
	}
}

func TestConnLimit_BanExpires(t *testing.T) {
	l := newConnectionRateLimiter(2, 10*time.Second, 30*time.Second)
	now := time.Now()
	l.allow("9.9.9.9", now)
	l.allow("9.9.9.9", now)
	l.allow("9.9.9.9", now) // 触发封禁
	// 封禁 30s 后解封。
	if !l.allow("9.9.9.9", now.Add(31*time.Second)) {
		t.Fatal("connection after ban expiry should be allowed")
	}
}

func TestConnLimit_WindowResets(t *testing.T) {
	l := newConnectionRateLimiter(2, 10*time.Second, 30*time.Second)
	now := time.Now()
	l.allow("5.5.5.5", now)
	l.allow("5.5.5.5", now)
	// 窗口过期后计数重置，又可连接。
	if !l.allow("5.5.5.5", now.Add(11*time.Second)) {
		t.Fatal("connection in a fresh window should be allowed")
	}
}

func TestConnLimit_PerIPIsolation(t *testing.T) {
	l := newConnectionRateLimiter(1, 10*time.Second, 30*time.Second)
	now := time.Now()
	l.allow("1.1.1.1", now)
	if l.allow("1.1.1.1", now) {
		t.Fatal("second connection from same IP should be rejected")
	}
	if !l.allow("2.2.2.2", now) { // 不同 IP 不受影响
		t.Fatal("different IP should be allowed")
	}
}

func TestConnLimit_Cleanup(t *testing.T) {
	l := newConnectionRateLimiter(2, 10*time.Second, 30*time.Second)
	now := time.Now()
	l.allow("1.1.1.1", now)
	l.allow("2.2.2.2", now)
	l.allow("2.2.2.2", now)
	l.allow("2.2.2.2", now) // 2.2.2.2 进入封禁
	l.cleanup(now.Add(time.Minute))
	l.mu.Lock()
	nw, nb := len(l.windows), len(l.banned)
	l.mu.Unlock()
	if nw != 0 || nb != 0 {
		t.Errorf("after cleanup expected empty maps, got windows=%d banned=%d", nw, nb)
	}
}
