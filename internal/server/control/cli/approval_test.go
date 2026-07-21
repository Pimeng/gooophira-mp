package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/core/server"
)

func addApproval(state *server.ServerState, ssid, ip string, status server.CLIApprovalStatus, ttl time.Duration) {
	state.Mu.Lock()
	state.CLIApprovalSessions[ssid] = &server.CLIApprovalSession{
		IP: ip, ExpiresAt: time.Now().Add(ttl).UnixMilli(), Status: status, RequestedAt: time.Now().UnixMilli(),
	}
	state.Mu.Unlock()
}

func TestCLI_Approve(t *testing.T) {
	c, state, buf := newTestConsole(t)
	addApproval(state, "abcd1234ef", "1.2.3.4", server.CLIApprovalPending, time.Minute)

	out := c.run(buf, "approve abcd1234") // 用短码
	if !strings.Contains(out, "Approved elevation request") {
		t.Fatalf("approve output = %q", out)
	}
	state.Mu.Lock()
	sess := state.CLIApprovalSessions["abcd1234ef"]
	tokenCount := len(state.TempAdminTokens)
	state.Mu.Unlock()
	if sess.Status != server.CLIApprovalApproved || sess.Token == "" {
		t.Errorf("session should be approved with a token, got status=%s token=%q", sess.Status, sess.Token)
	}
	if tokenCount != 1 {
		t.Errorf("a temp admin token should be issued, got %d", tokenCount)
	}
}

func TestCLI_ApproveAmbiguousAndNotFound(t *testing.T) {
	c, state, buf := newTestConsole(t)
	addApproval(state, "aa11", "ip", server.CLIApprovalPending, time.Minute)
	addApproval(state, "aa22", "ip", server.CLIApprovalPending, time.Minute)

	if out := c.run(buf, "approve aa"); !strings.Contains(out, "matches multiple") {
		t.Errorf("ambiguous prefix should warn, got %q", out)
	}
	if out := c.run(buf, "approve zzzz"); !strings.Contains(out, "No pending elevation request matched") {
		t.Errorf("unknown prefix should report not found, got %q", out)
	}
}

func TestCLI_Deny(t *testing.T) {
	c, state, buf := newTestConsole(t)
	addApproval(state, "deny0001", "9.9.9.9", server.CLIApprovalPending, time.Minute)

	if out := c.run(buf, "deny deny0001"); !strings.Contains(out, "Denied elevation request") {
		t.Fatalf("deny output = %q", out)
	}
	state.Mu.Lock()
	st := state.CLIApprovalSessions["deny0001"].Status
	state.Mu.Unlock()
	if st != server.CLIApprovalDenied {
		t.Errorf("session should be denied, got %s", st)
	}
}

func TestCLI_ApproveAlreadyHandled(t *testing.T) {
	c, state, buf := newTestConsole(t)
	addApproval(state, "done0001", "ip", server.CLIApprovalDenied, time.Minute)
	if out := c.run(buf, "approve done0001"); !strings.Contains(out, "already in") {
		t.Errorf("handling an already-denied session should warn, got %q", out)
	}
}

func TestCLI_ApproveExpired(t *testing.T) {
	c, state, buf := newTestConsole(t)
	addApproval(state, "old00001", "ip", server.CLIApprovalPending, -time.Second) // 已过期
	if out := c.run(buf, "approve old00001"); !strings.Contains(out, "has expired") {
		t.Errorf("expired session should report expired, got %q", out)
	}
}

func TestCLI_Pending(t *testing.T) {
	c, state, buf := newTestConsole(t)
	if out := c.run(buf, "pending"); !strings.Contains(out, "No pending CLI elevation requests") {
		t.Errorf("empty pending = %q", out)
	}
	addApproval(state, "pend0001", "5.6.7.8", server.CLIApprovalPending, time.Minute)
	addApproval(state, "appr0001", "1.1.1.1", server.CLIApprovalApproved, time.Minute) // 非 pending 不列出
	out := c.run(buf, "pending")
	if !strings.Contains(out, "Pending CLI elevation requests (1)") {
		t.Errorf("pending header wrong: %q", out)
	}
	if !strings.Contains(out, "pend0001") || strings.Contains(out, "appr0001") {
		t.Errorf("pending should list only the pending session: %q", out)
	}
}

func TestCLI_ApproveUsage(t *testing.T) {
	c, _, buf := newTestConsole(t)
	if out := c.run(buf, "approve"); !strings.Contains(out, "Usage: approve") {
		t.Errorf("approve with no args should show usage, got %q", out)
	}
}
