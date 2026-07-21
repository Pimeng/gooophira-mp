package load

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildMigrationPlanSplitsEnabledExtensions(t *testing.T) {
	legacy := writeTempYAML(t, `
SERVER_NAME: Test
PORT: 12346
REPLAY_ENABLED: true
REPLAY_TTL_DAYS: 9
REDIS:
  ENABLED: false
WEBHOOK:
  ENABLED: true
  TARGETS: []
OUTBOUND_PROXY: false
`)
	plan, err := BuildMigrationPlan(legacy)
	if err != nil {
		t.Fatalf("BuildMigrationPlan: %v", err)
	}
	want := map[string]bool{
		CoreConfigFile: true, "network.yaml": true, "replay.yaml": true,
		"agent.yaml": true,
	}
	for name := range want {
		if _, present := plan.Files[name]; !present {
			t.Errorf("missing %s", name)
		}
	}
	if _, present := plan.Files["redis.yaml"]; present {
		t.Error("disabled Redis should not be migrated")
	}
	for _, legacyName := range []string{"webhook.yaml", "stats.yaml"} {
		if _, present := plan.Files[legacyName]; present {
			t.Errorf("deprecated %s should be folded into agent.yaml", legacyName)
		}
	}

	dir := filepath.Join(t.TempDir(), "config")
	if err := plan.Write(dir); err != nil {
		t.Fatalf("Write: %v", err)
	}
	set, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("migrated config does not load: %v", err)
	}
	if !set.Config.EffectiveReplayEnabled() || set.Config.EffectiveReplayTTLDays() != 9 {
		t.Errorf("replay was not preserved: %+v", set.Config)
	}
	if set.Config.EffectiveAgentIPC().Endpoint != "auto" {
		t.Errorf("Agent IPC was not enabled: %+v", set.Config.EffectiveAgentIPC())
	}
	agent, err := LoadAgentFile(filepath.Join(dir, "agent.yaml"))
	if err != nil {
		t.Fatalf("migrated Agent config does not load: %v", err)
	}
	if agent.Webhook == nil || !agent.Webhook.Enabled || !agent.Stats.Enabled {
		t.Errorf("Agent extensions were not preserved: %+v", agent)
	}
}

func TestBuildMigrationPlanMovesReplayUploadToAgent(t *testing.T) {
	legacy := writeTempYAML(t, `
REPLAY_ENABLED: true
REPLAY_BASE_DIR: ./record
REPLAY_AUTO_UPLOAD: true
SHARE_STATION:
  URL: https://replay.example.com
  TOKEN: secret
`)
	plan, err := BuildMigrationPlan(legacy)
	if err != nil {
		t.Fatalf("BuildMigrationPlan: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "config")
	if err := plan.Write(dir); err != nil {
		t.Fatalf("Write: %v", err)
	}
	agent, err := LoadAgentFile(filepath.Join(dir, "agent.yaml"))
	if err != nil {
		t.Fatalf("LoadAgentFile: %v", err)
	}
	if !agent.ReplayUpload.Enabled || !agent.ReplayUpload.AutoUpload || agent.ReplayUpload.BaseDir != "./record" || agent.ReplayUpload.Token != "secret" {
		t.Fatalf("replay upload was not migrated: %+v", agent.ReplayUpload)
	}
}

func TestMigrationPlanRefusesOverwriteBeforeWriting(t *testing.T) {
	plan := &MigrationPlan{Files: map[string][]byte{
		CoreConfigFile: []byte("version: 1\n"),
		"agent.yaml":   []byte("version: 1\n"),
	}}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := plan.Write(dir); err == nil {
		t.Fatal("Write should refuse existing target")
	}
	if _, err := os.Stat(filepath.Join(dir, CoreConfigFile)); !os.IsNotExist(err) {
		t.Fatal("preflight conflict should prevent all writes")
	}
}

func TestBuildMigrationPlanRejectsInvalidLegacyValue(t *testing.T) {
	legacy := writeTempYAML(t, "PORT: nope\n")
	if _, err := BuildMigrationPlan(legacy); err == nil {
		t.Fatal("invalid legacy value should fail migration")
	}
}
