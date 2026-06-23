package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const sampleYAML = `
HOST: "::"
PORT: 12346
ROOM_MAX_USERS: 12
CHAT_ENABLED: true
REPLAY_ENABLED: false
LOG_LEVEL: INFO
MONITORS:
  - 2
  - 300
CORS_ORIGINS:
  - "https://t.phira.link"
SHARE_STATION:
  URL: "http://example.com"
  TOKEN: "tok"
REDIS:
  ENABLED: true
  HOST: "127.0.0.1"
  PORT: 6380
  DB: 1
`

func writeTempYAML(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cfg.yml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write temp yaml: %v", err)
	}
	return path
}

func TestLoadFile(t *testing.T) {
	cfg, err := LoadFile(writeTempYAML(t, sampleYAML))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if cfg.Host == nil || *cfg.Host != "::" {
		t.Errorf("HOST = %v, want ::", cfg.Host)
	}
	if cfg.Port == nil || *cfg.Port != 12346 {
		t.Errorf("PORT = %v, want 12346", cfg.Port)
	}
	if cfg.EffectiveRoomMaxUsers() != 12 {
		t.Errorf("ROOM_MAX_USERS = %d, want 12", cfg.EffectiveRoomMaxUsers())
	}
	if !cfg.EffectiveChatEnabled() {
		t.Error("CHAT_ENABLED should be true")
	}
	if !reflect.DeepEqual(cfg.Monitors, []int{2, 300}) {
		t.Errorf("MONITORS = %v, want [2 300]", cfg.Monitors)
	}
	if !reflect.DeepEqual(cfg.CorsOrigins, []string{"https://t.phira.link"}) {
		t.Errorf("CORS_ORIGINS = %v", cfg.CorsOrigins)
	}
	if cfg.ShareStation == nil || cfg.ShareStation.URL != "http://example.com" || cfg.ShareStation.Token != "tok" {
		t.Errorf("SHARE_STATION = %+v", cfg.ShareStation)
	}
	if cfg.Redis == nil || !cfg.Redis.Enabled || cfg.Redis.Port != 6380 || cfg.Redis.DB != 1 {
		t.Errorf("REDIS = %+v", cfg.Redis)
	}
}

func TestLoadMerged_EnvOverridesFile(t *testing.T) {
	path := writeTempYAML(t, "PORT: 12346\nROOM_MAX_USERS: 12\n")
	t.Setenv("PORT", "9999") // env 应覆盖文件
	cfg, fromFile, err := LoadMerged(path)
	if err != nil {
		t.Fatalf("LoadMerged: %v", err)
	}
	if !fromFile {
		t.Error("fromFile should be true")
	}
	if cfg.Port == nil || *cfg.Port != 9999 {
		t.Errorf("env PORT should override file: got %v, want 9999", cfg.Port)
	}
	if cfg.EffectiveRoomMaxUsers() != 12 {
		t.Errorf("file ROOM_MAX_USERS should remain: got %d", cfg.EffectiveRoomMaxUsers())
	}
}

func TestLoadMerged_MissingFileUsesDefaults(t *testing.T) {
	cfg, fromFile, err := LoadMerged(filepath.Join(t.TempDir(), "nonexistent.yml"))
	if err != nil {
		t.Fatalf("LoadMerged on missing file should not error: %v", err)
	}
	if fromFile {
		t.Error("fromFile should be false for missing file")
	}
	if !reflect.DeepEqual(cfg.Monitors, []int{2}) {
		t.Errorf("missing file should fall back to default monitors [2], got %v", cfg.Monitors)
	}
}
