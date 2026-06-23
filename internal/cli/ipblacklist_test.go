package cli

import (
	"strings"
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/logging"
)

func TestCLI_IPBlacklist(t *testing.T) {
	c, state, buf := newTestConsole(t)
	lg := logging.New("INFO", "")
	state.Logger = lg

	// 触发把 1.2.3.4 拉黑（超过阈值的连接日志）。
	for range 11 {
		lg.ConnectionLog("1.2.3.4", "x")
	}

	out := c.run(buf, "ipblacklist list")
	if !strings.Contains(out, "1.2.3.4") {
		t.Fatalf("blacklist list should show 1.2.3.4, got %q", out)
	}

	if out := c.run(buf, "ipblacklist remove 1.2.3.4"); !strings.Contains(out, "1.2.3.4") {
		t.Errorf("remove output should mention the IP, got %q", out)
	}
	if out := c.run(buf, "ipblacklist list"); !strings.Contains(out, "No blacklisted") && !strings.Contains(out, "empty") {
		// en-US: cli-blacklist-empty
		if !strings.Contains(strings.ToLower(out), "no ") {
			t.Errorf("after remove, list should be empty, got %q", out)
		}
	}
}

func TestCLI_IPBlacklistClearAndUsage(t *testing.T) {
	c, state, buf := newTestConsole(t)
	lg := logging.New("INFO", "")
	state.Logger = lg
	for range 11 {
		lg.ConnectionLog("8.8.8.8", "x")
	}
	if out := c.run(buf, "ipblacklist clear"); out == "" {
		t.Error("clear should print confirmation")
	}
	if len(lg.GetBlacklistedIPs()) != 0 {
		t.Error("clear should empty the blacklist")
	}
	// 无子命令 → 用法。
	if out := c.run(buf, "ipblacklist"); !strings.Contains(strings.ToLower(out), "usage") {
		t.Errorf("no subcommand should show usage, got %q", out)
	}
	// 未知子命令。
	if out := c.run(buf, "ipblacklist bogus"); out == "" {
		t.Error("unknown subcommand should print an error")
	}
}
