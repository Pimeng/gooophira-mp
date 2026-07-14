package config

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
		"webhook.yaml": true, "stats.yaml": true,
	}
	for name := range want {
		if _, present := plan.Files[name]; !present {
			t.Errorf("missing %s", name)
		}
	}
	if _, present := plan.Files["redis.yaml"]; present {
		t.Error("disabled Redis should not be migrated")
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
	if set.Config.Webhook == nil || !set.Config.Webhook.Enabled {
		t.Errorf("webhook was not preserved: %+v", set.Config.Webhook)
	}
}

func TestMigrationPlanRefusesOverwriteBeforeWriting(t *testing.T) {
	plan := &MigrationPlan{Files: map[string][]byte{
		CoreConfigFile: []byte("version: 1\n"),
		"stats.yaml":   []byte("version: 1\n"),
	}}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stats.yaml"), []byte("existing"), 0o644); err != nil {
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
