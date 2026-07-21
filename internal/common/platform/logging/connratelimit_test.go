package logging

import "testing"

func TestConnRateLimiter_BlacklistsAfterThreshold(t *testing.T) {
	l := newConnRateLimiter()
	// 前 threshold 条放行。
	for i := range connLogThreshold {
		if !l.shouldLog("1.2.3.4") {
			t.Fatalf("connection log %d under threshold should be allowed", i+1)
		}
	}
	// 第 threshold+1 条触发封禁。
	if l.shouldLog("1.2.3.4") {
		t.Fatal("log over threshold should be suppressed")
	}
	// 黑名单内继续抑制。
	if l.shouldLog("1.2.3.4") {
		t.Fatal("blacklisted IP should stay suppressed")
	}
	bl := l.getBlacklisted()
	if len(bl) != 1 || bl[0].IP != "1.2.3.4" || bl[0].ExpiresIn <= 0 {
		t.Fatalf("blacklist should contain 1.2.3.4 with positive TTL, got %v", bl)
	}
}

func TestConnRateLimiter_RemoveAndClear(t *testing.T) {
	l := newConnRateLimiter()
	for range connLogThreshold + 1 {
		l.shouldLog("5.5.5.5")
	}
	if len(l.getBlacklisted()) != 1 {
		t.Fatal("setup: 5.5.5.5 should be blacklisted")
	}
	l.remove("5.5.5.5")
	if len(l.getBlacklisted()) != 0 {
		t.Error("remove should unblacklist the IP")
	}
	// 解封后又可记录。
	if !l.shouldLog("5.5.5.5") {
		t.Error("after remove, logging should be allowed again")
	}

	for range connLogThreshold + 1 {
		l.shouldLog("6.6.6.6")
	}
	l.clear()
	if len(l.getBlacklisted()) != 0 {
		t.Error("clear should empty the blacklist")
	}
}

func TestConnRateLimiter_PerIPIsolation(t *testing.T) {
	l := newConnRateLimiter()
	for range connLogThreshold + 1 {
		l.shouldLog("1.1.1.1")
	}
	// 另一个 IP 不受影响。
	if !l.shouldLog("2.2.2.2") {
		t.Error("a different IP should not be affected by another's blacklist")
	}
}

func TestLogger_ConnectionLogBlacklistAPI(t *testing.T) {
	lg := New("INFO", "") // Debug 低于 INFO → 不打印；限流仍累计
	for range connLogThreshold + 1 {
		lg.ConnectionLog("9.9.9.9", "new conn")
	}
	if ips := lg.GetBlacklistedIPs(); len(ips) != 1 || ips[0].IP != "9.9.9.9" {
		t.Fatalf("logger should expose blacklisted 9.9.9.9, got %v", ips)
	}
	lg.RemoveFromBlacklist("9.9.9.9")
	if len(lg.GetBlacklistedIPs()) != 0 {
		t.Error("RemoveFromBlacklist should clear the entry")
	}
	// 空 IP 不抑制、不入黑名单。
	lg.ConnectionLog("", "x")
	if len(lg.GetBlacklistedIPs()) != 0 {
		t.Error("empty IP should not be tracked")
	}
}
