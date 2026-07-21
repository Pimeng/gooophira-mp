package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/config"
)

func TestAdminData_SaveLoadRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admin_data.json")
	cfg := &config.ServerConfig{}

	st := NewServerState(cfg, nil, "test", path, "")
	st.BannedUsers[100] = struct{}{}
	st.BannedUsers[200] = struct{}{}
	st.BannedRoomUsers["room1"] = map[int]struct{}{5: {}, 6: {}}
	if err := st.SaveAdminData(); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("admin data file not written: %v", err)
	}

	// 载入到新的 state。
	st2 := NewServerState(cfg, nil, "test", path, "")
	if err := st2.LoadAdminData(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, ok := st2.BannedUsers[100]; !ok {
		t.Error("user 100 ban not loaded")
	}
	if _, ok := st2.BannedUsers[200]; !ok {
		t.Error("user 200 ban not loaded")
	}
	set := st2.BannedRoomUsers["room1"]
	if set == nil || len(set) != 2 {
		t.Fatalf("room1 bans not loaded: %v", set)
	}
	if _, ok := set[5]; !ok {
		t.Error("room1 ban of user 5 not loaded")
	}
}

func TestAdminData_LoadMissingFileIsNoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.json")
	st := NewServerState(&config.ServerConfig{}, nil, "test", path, "")
	if err := st.LoadAdminData(); err != nil {
		t.Errorf("loading missing file should not error: %v", err)
	}
	if len(st.BannedUsers) != 0 {
		t.Error("no bans expected from missing file")
	}
}
