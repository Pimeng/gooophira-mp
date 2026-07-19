package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadWebhookFileForAgent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent_config.yml")
	body := "version: 1\nENABLED: true\nTIMEOUT_MS: 1000\nRETRIES: 1\nTARGETS:\n  - ID: primary\n    TYPE: generic\n    URL: https://example.test/hook\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWebhookFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.TimeoutMS != 1000 || len(cfg.Targets) != 1 || cfg.Targets[0].Type != "generic" || cfg.Targets[0].ID != "primary" {
		t.Fatalf("loaded Agent webhook config = %+v", cfg)
	}
}

func TestAgentConfigExampleLoads(t *testing.T) {
	cfg, err := LoadAgentFile(filepath.Join("..", "..", "config.example", "agent.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Webhook.Enabled || len(cfg.Webhook.Targets) != 0 || cfg.Stats.Enabled || cfg.Stats.DBPath != "stats.db" {
		t.Fatalf("Agent example should be disabled and empty: %+v", cfg)
	}
}

func TestLoadAgentFileStats(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yml")
	body := "version: 1\nENABLED: false\nTARGETS: []\nSTATS:\n  ENABLED: true\n  DB_PATH: agent-stats.db\n  DETAIL_RETENTION_DAYS: 30\n  DB_MAX_MB: 100\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadAgentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Stats.Enabled || cfg.Stats.DBPath != "agent-stats.db" || cfg.Stats.DetailRetentionDays != 30 || cfg.Stats.DBMaxMB != 100 {
		t.Fatalf("Agent stats config = %+v", cfg.Stats)
	}
}

func TestLoadAgentFileReplayUpload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yml")
	body := "version: 1\nENABLED: false\nTARGETS: []\nREPLAY_UPLOAD:\n  ENABLED: true\n  AUTO_UPLOAD: true\n  BASE_DIR: replays\n  URL: https://share.example/\n  TOKEN: secret\n  STATE_PATH: upload.json\n  DELAY_MS: 10\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadAgentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.ReplayUpload
	if !got.Enabled || !got.AutoUpload || got.URL != "https://share.example" || got.DelayMS != 10 || got.StatePath != "upload.json" {
		t.Fatalf("Agent replay upload config = %+v", got)
	}
}

func TestLoadAgentFileDocumentationExample(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	body := `version: 1
ENABLED: true
TIMEOUT_MS: 5000
RETRIES: 2
TARGETS:
  - ID: primary-generic
    TYPE: generic
    URL: https://example.com/hook
    SECRET: replace_me
    EVENTS: [room_create, game_start, game_end]
STATS:
  ENABLED: true
  DB_PATH: data/stats.db
  DETAIL_RETENTION_DAYS: 90
  DB_MAX_MB: 500
REPLAY_UPLOAD:
  ENABLED: true
  AUTO_UPLOAD: true
  BASE_DIR: ./record
  URL: https://replay.example.com
  TOKEN: replace_me
  STATE_PATH: agent-inbox/upload-state.json
  DELAY_MS: 30000
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadAgentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Webhook.Enabled || len(cfg.Webhook.Targets) != 1 || !cfg.Stats.Enabled || !cfg.ReplayUpload.Enabled {
		t.Fatalf("documentation example did not enable all Agent extensions: %+v", cfg)
	}
}
