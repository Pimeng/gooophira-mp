package cli

import (
	"strings"
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/core/server"
)

func TestNewExecutor_CapturesOutput(t *testing.T) {
	en := "en-US"
	state := server.NewServerState(&config.ServerConfig{Lang: &en}, nil, "test", "", "")
	exec := NewExecutor(state, server.NewHub(state, nil), nil)

	lines, err := exec("list") // 无房间 → 一行 "No rooms currently..."
	if err != nil {
		t.Fatalf("exec error: %v", err)
	}
	if len(lines) == 0 || lines[0].Kind != "info" {
		t.Fatalf("expected captured info line (empty room list), got %v", lines)
	}
	if !strings.Contains(lines[0].Text, "No rooms") {
		t.Errorf("unexpected output: %q", lines[0].Text)
	}
}

func TestNewExecutor_OutputKinds(t *testing.T) {
	en := "en-US"
	state := server.NewServerState(&config.ServerConfig{Lang: &en}, nil, "test", "", "")
	exec := NewExecutor(state, server.NewHub(state, nil), nil)

	kindOf := func(cmd string) string {
		lines, _ := exec(cmd)
		if len(lines) == 0 {
			return ""
		}
		return lines[0].Kind
	}

	// 用法错误 → error；成功操作 → success；未找到 → error。
	if k := kindOf("ban"); k != "error" { // 缺参数 → 用法错误
		t.Errorf("ban usage kind = %q, want error", k)
	}
	if k := kindOf("ban 5"); k != "success" { // 封禁成功
		t.Errorf("ban success kind = %q, want success", k)
	}
	if k := kindOf("user 999"); k != "error" { // 用户不存在
		t.Errorf("user-not-found kind = %q, want error", k)
	}

	// user <id> 的详情应为中性 out 行（首行为空行 + 头部）。
	addRoom(state, "r1", 7)
	lines, _ := exec("user 7")
	if len(lines) == 0 {
		t.Fatal("user info should produce output")
	}
	for _, ln := range lines {
		if ln.Kind != "out" {
			t.Errorf("user info line kind = %q, want out (%q)", ln.Kind, ln.Text)
		}
	}
}

func TestNewExecutor_ShutdownCommand(t *testing.T) {
	en := "en-US"
	state := server.NewServerState(&config.ServerConfig{Lang: &en}, nil, "test", "", "")
	stopped := false
	exec := NewExecutor(state, server.NewHub(state, nil), func() { stopped = true })
	if _, err := exec("stop"); err != nil {
		t.Fatalf("exec stop: %v", err)
	}
	if !stopped {
		t.Error("stop command via executor should trigger shutdown")
	}
}
