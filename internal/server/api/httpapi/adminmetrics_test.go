package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/config"
)

func TestAdminMetrics(t *testing.T) {
	svc, state := newTestService(t, adminCfg())
	addUser(state, 1, "alice", &fakeSession{id: "s1"})
	addRoom(state, "room1", 2, "bob", &config.Chart{ID: 9, Name: "c"})
	state.Mu.Lock()
	state.BannedUsers[5] = struct{}{}
	state.Mu.Unlock()

	w := doAuth(svc, http.MethodGet, "/admin/metrics", "secret", "")
	if w.Code != 200 {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		OK      bool `json:"ok"`
		Process struct {
			NumCPU     int     `json:"numCPU"`
			Goroutines int     `json:"goroutines"`
			Uptime     float64 `json:"uptime"`
			Runtime    string  `json:"runtime"`
		} `json:"process"`
		Memory struct {
			Sys         uint64 `json:"sys"`
			RSS         uint64 `json:"rss"`
			HeapUsed    uint64 `json:"heapUsed"`
			HeapTotal   uint64 `json:"heapTotal"`
			SystemTotal uint64 `json:"systemTotal"`
		} `json:"memory"`
		CPU struct {
			Cores   int     `json:"cores"`
			Percent float64 `json:"percent"`
		} `json:"cpu"`
		Agent struct {
			Enabled       bool  `json:"enabled"`
			Online        bool  `json:"online"`
			PendingEvents int   `json:"pendingEvents"`
			OutboxBytes   int64 `json:"outboxBytes"`
		} `json:"agent"`
		Business struct {
			OnlineUsers       int  `json:"onlineUsers"`
			ActiveRooms       int  `json:"activeRooms"`
			ServerBannedUsers int  `json:"serverBannedUsers"`
			ReplayEnabled     bool `json:"replayEnabled"`
		} `json:"business"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("not ok: %s", w.Body.String())
	}
	if resp.Process.NumCPU < 1 || resp.Process.Goroutines < 1 {
		t.Errorf("process metrics look wrong: %+v", resp.Process)
	}
	if resp.Memory.Sys == 0 {
		t.Error("memory.sys should be > 0")
	}
	// GUI 图表契约字段。
	if resp.Memory.RSS == 0 || resp.Memory.HeapUsed == 0 || resp.Memory.HeapTotal == 0 {
		t.Errorf("GUI memory fields should be non-zero: %+v", resp.Memory)
	}
	if resp.CPU.Cores < 1 {
		t.Errorf("cpu.cores should be >= 1, got %d", resp.CPU.Cores)
	}
	if resp.Process.Runtime == "" {
		t.Error("process.runtime should be set for GUI subtitle")
	}
	if resp.Agent.Enabled || resp.Agent.Online {
		t.Errorf("Agent should be observably disabled in the default test service: %+v", resp.Agent)
	}
	// addUser(1) + addRoom 的房主(2) = 2 名在线用户。
	if resp.Business.OnlineUsers != 2 || resp.Business.ActiveRooms != 1 || resp.Business.ServerBannedUsers != 1 {
		t.Errorf("business metrics wrong: %+v", resp.Business)
	}
}

func TestAdminMetrics_History(t *testing.T) {
	svc, _ := newTestService(t, adminCfg())
	w := doAuth(svc, http.MethodGet, "/admin/metrics?history=1", "secret", "")
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	var resp struct {
		History []struct {
			Timestamp  int64   `json:"timestamp"`
			CPUPercent float64 `json:"cpuPercent"`
			RSS        uint64  `json:"rss"`
		} `json:"history"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	// 采样器在 New() 时即取首帧，?history=1 至少应有 1 个采样点。
	if len(resp.History) == 0 {
		t.Fatal("history should contain at least the initial sample")
	}
	if resp.History[0].Timestamp == 0 || resp.History[0].RSS == 0 {
		t.Errorf("history sample looks empty: %+v", resp.History[0])
	}

	// 不带 history 参数时应无 history 字段。
	w2 := doAuth(svc, http.MethodGet, "/admin/metrics", "secret", "")
	if strings.Contains(w2.Body.String(), "\"history\"") {
		t.Error("history should be omitted without ?history=1")
	}
}

func TestAdminMetrics_RequiresAuth(t *testing.T) {
	svc, _ := newTestService(t, adminCfg())
	if w := doAuth(svc, http.MethodGet, "/admin/metrics", "wrong", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("metrics without valid token should be 401, got %d", w.Code)
	}
}
