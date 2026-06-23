package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/server"
)

func TestAdminConsole_Logs(t *testing.T) {
	svc, state := newTestService(t, adminCfg())
	state.ConsoleHub.Push("INFO", "hello world")
	state.ConsoleHub.Push("WARN", "watch out")

	w := doAuth(svc, http.MethodGet, "/admin/console/logs?limit=10", "secret", "")
	if w.Code != 200 {
		t.Fatalf("logs status = %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		OK    bool `json:"ok"`
		Lines []struct {
			Level     string `json:"level"`
			Message   string `json:"message"`
			Timestamp int64  `json:"timestamp"`
		} `json:"lines"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.OK || len(resp.Lines) != 2 || resp.Lines[1].Message != "watch out" {
		t.Fatalf("unexpected logs response: %s", w.Body.String())
	}
	if resp.Lines[1].Level != "WARN" || resp.Lines[1].Timestamp == 0 {
		t.Errorf("log line should carry level+timestamp: %+v", resp.Lines[1])
	}
}

func TestAdminConsole_Command(t *testing.T) {
	svc, state := newTestService(t, adminCfg())
	// 注入一个简单执行器：回显命令。
	state.ConsoleExecutor = func(line string) ([]server.ConsoleOutputLine, error) {
		return []server.ConsoleOutputLine{{Kind: "out", Text: "ran: " + line}}, nil
	}

	w := doAuth(svc, http.MethodPost, "/admin/console/command", "secret", `{"command":"list"}`)
	if w.Code != 200 {
		t.Fatalf("command status = %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		OK    bool `json:"ok"`
		Lines []struct {
			Text string `json:"text"`
		} `json:"lines"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.OK || len(resp.Lines) != 1 || resp.Lines[0].Text != "ran: list" {
		t.Fatalf("unexpected command response: %s", w.Body.String())
	}

	// 空命令 → 400。
	if w := doAuth(svc, http.MethodPost, "/admin/console/command", "secret", `{"command":"  "}`); w.Code != 400 {
		t.Errorf("empty command should be 400, got %d", w.Code)
	}
}

func TestAdminConsole_CommandNotReady(t *testing.T) {
	svc, _ := newTestService(t, adminCfg()) // 无 ConsoleExecutor
	if w := doAuth(svc, http.MethodPost, "/admin/console/command", "secret", `{"command":"list"}`); w.Code != http.StatusServiceUnavailable {
		t.Errorf("command without executor should be 503, got %d", w.Code)
	}
}
