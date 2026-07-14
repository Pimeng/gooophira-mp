package server

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
	"github.com/Pimeng/gooophira-mp/internal/replay"
)

// fakeRecorder 是用于测试的最小 ReplayRecorder，记录 EndRoom 与 SetBaseDir 调用。
type fakeRecorder struct {
	baseDir  string
	endRooms []protocol.RoomID
}

func (f *fakeRecorder) SetBaseDir(dir string)                                     { f.baseDir = dir }
func (f *fakeRecorder) AppendTouches(protocol.RoomID, int, []protocol.TouchFrame) {}
func (f *fakeRecorder) AppendJudges(protocol.RoomID, int, []protocol.JudgeEvent)  {}
func (f *fakeRecorder) SetRecordID(protocol.RoomID, int, int)                     {}
func (f *fakeRecorder) EndRoom(id protocol.RoomID)                                { f.endRooms = append(f.endRooms, id) }
func (f *fakeRecorder) FakeMonitorInfo(name string) protocol.UserInfo {
	return protocol.UserInfo{ID: replay.FakeMonitorID(), Name: name, Monitor: true}
}

func TestReloadConfig_ListenerAndChangedKeys(t *testing.T) {
	st := NewServerState(&config.ServerConfig{}, nil, "test", "", "")
	var got *config.ServerConfig
	st.OnConfigReload(func(c *config.ServerConfig) { got = c })

	next := &config.ServerConfig{}
	chatOff := false
	next.ChatEnabled = &chatOff
	changed, restart := st.ReloadConfig(next)

	if len(restart) != 0 {
		t.Errorf("no startup-only change expected, got restart=%v", restart)
	}
	if !contains(changed, "CHAT_ENABLED") {
		t.Errorf("CHAT_ENABLED should be in changed keys, got %v", changed)
	}
	if got == nil {
		t.Fatal("config reload listener not invoked")
	}
	if got.EffectiveChatEnabled() {
		t.Error("listener received config with chat still enabled")
	}
}

func TestReloadConfig_NoChangeIsNoop(t *testing.T) {
	st := NewServerState(&config.ServerConfig{}, nil, "test", "", "")
	called := false
	st.OnConfigReload(func(*config.ServerConfig) { called = true })
	changed, restart := st.ReloadConfig(&config.ServerConfig{})
	if len(changed) != 0 || len(restart) != 0 {
		t.Errorf("identical config should yield no changes, got changed=%v restart=%v", changed, restart)
	}
	if called {
		t.Error("listener should not be called on no-op reload")
	}
}

func TestReloadConfig_StartupOnlyReportedNotApplied(t *testing.T) {
	host := "0.0.0.0"
	st := NewServerState(&config.ServerConfig{Host: &host}, nil, "test", "", "")
	newHost := "127.0.0.1"
	next := &config.ServerConfig{Host: &newHost}
	changed, restart := st.ReloadConfig(next)
	if !contains(restart, "HOST") {
		t.Errorf("HOST change should require restart, got %v", restart)
	}
	if contains(changed, "HOST") {
		t.Errorf("startup-only HOST should not be in applied changes, got %v", changed)
	}
	// 实际生效配置仍是旧 host。
	if st.Config.Host == nil || *st.Config.Host != "0.0.0.0" {
		t.Errorf("HOST should not be applied at runtime, got %v", st.Config.Host)
	}
}

func TestReloadConfig_ReplayDisableEndsRooms(t *testing.T) {
	enabled := true
	st := NewServerState(&config.ServerConfig{ReplayEnabled: &enabled}, nil, "test", "", "")
	rec := &fakeRecorder{}
	st.ReplayRecorder = rec
	// 放入一个可录制的房间。
	room := NewRoom(protocol.RoomID("room1"), 100, 8, true)
	room.RefreshLive(true)
	st.Rooms[room.ID] = room

	// 关闭回放 → 应结束进行中的录制。
	disabled := false
	st.ReloadConfig(&config.ServerConfig{ReplayEnabled: &disabled})

	if st.ReplayEnabled {
		t.Fatal("ReplayEnabled flag should be false after reload")
	}
	if len(rec.endRooms) != 1 || rec.endRooms[0] != room.ID {
		t.Errorf("expected EndRoom(room1), got %v", rec.endRooms)
	}
	if room.Live {
		t.Error("room live should be recomputed to false (no monitors, replay off)")
	}
}

func TestApplyRuntimePatch_PersistsAndApplies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server_config.yml")
	if err := os.WriteFile(path, []byte("# cfg\nREPLAY_ENABLED: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := NewServerState(&config.ServerConfig{}, nil, "test", "", path)

	res := config.ParseRuntimeConfigPatch(map[string]any{"REPLAY_ENABLED": true})
	if !res.OK {
		t.Fatal("patch should be ok")
	}
	changed, _ := st.ApplyRuntimePatch(res)

	if !contains(changed, "REPLAY_ENABLED") {
		t.Errorf("REPLAY_ENABLED should be changed, got %v", changed)
	}
	if !st.ReplayEnabled {
		t.Error("live state ReplayEnabled should be true after patch")
	}
	// 落盘并保留注释。
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "REPLAY_ENABLED: true") {
		t.Errorf("config file not updated:\n%s", raw)
	}
	if !strings.Contains(string(raw), "# cfg") {
		t.Errorf("comment not preserved:\n%s", raw)
	}
	// 回滚快照记录了变更前的值（false）。
	if st.LastRuntimeConfigRollback["REPLAY_ENABLED"] != false {
		t.Errorf("rollback snapshot should capture prior false, got %v", st.LastRuntimeConfigRollback["REPLAY_ENABLED"])
	}
}

func TestApplyRuntimePatch_PersistsToConfigDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, config.CoreConfigFile), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := NewServerState(&config.ServerConfig{}, nil, "test", "", "")
	st.ConfigDir = dir

	res := config.ParseRuntimeConfigPatch(map[string]any{
		"ROOM_CREATION_ENABLED": false,
		"REPLAY_ENABLED":        true,
	})
	if !res.OK {
		t.Fatal("patch should be valid")
	}
	st.ApplyRuntimePatch(res)

	serverRaw, _ := os.ReadFile(filepath.Join(dir, config.CoreConfigFile))
	if !strings.Contains(string(serverRaw), "ROOM_CREATION_ENABLED: false") {
		t.Fatalf("server.yaml not updated:\n%s", serverRaw)
	}
	replayRaw, _ := os.ReadFile(filepath.Join(dir, "replay.yaml"))
	if !strings.Contains(string(replayRaw), "version: 1") || !strings.Contains(string(replayRaw), "REPLAY_ENABLED: true") {
		t.Fatalf("replay.yaml not created correctly:\n%s", replayRaw)
	}
}

// contains 在 reload_test 与其它测试间共用（定义在本文件）。
func contains(s []string, v string) bool {
	return slices.Contains(s, v)
}
