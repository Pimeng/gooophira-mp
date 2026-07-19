package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfigFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestLoadDirRequiresServer(t *testing.T) {
	_, err := LoadDir(t.TempDir())
	if !os.IsNotExist(err) {
		t.Fatalf("LoadDir error = %v, want not exist", err)
	}
}

func TestLoadDirOptionalFilesEnableCapabilities(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, CoreConfigFile, "version: 1\nPORT: 12346\n")

	set, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir core: %v", err)
	}
	if set.Config.EffectiveReplayEnabled() || set.Config.Redis != nil || set.Config.Webhook != nil {
		t.Fatalf("extensions should be absent: %+v", set.Config)
	}

	writeConfigFile(t, dir, "replay.yaml", "version: 1\nREPLAY_TTL_DAYS: 7\n")
	writeConfigFile(t, dir, "redis.yaml", "version: 1\nHOST: redis\nPORT: 6379\n")
	writeConfigFile(t, dir, "webhook.yaml", "version: 1\nTARGETS: []\n")
	set, err = LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir extensions: %v", err)
	}
	if !set.Config.EffectiveReplayEnabled() || set.Config.EffectiveReplayTTLDays() != 7 {
		t.Errorf("replay config not enabled: %+v", set.Config)
	}
	if set.Config.Redis == nil || !set.Config.Redis.Enabled || set.Config.Redis.Host != "redis" {
		t.Errorf("redis config = %+v", set.Config.Redis)
	}
	if set.Config.Webhook == nil || !set.Config.Webhook.Enabled {
		t.Errorf("webhook config = %+v", set.Config.Webhook)
	}
}

func TestLoadDirRejectsUnknownAndInvalidValues(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, CoreConfigFile, "version: 1\nPROT: 12346\n")
	if _, err := LoadDir(dir); err == nil || !strings.Contains(err.Error(), "PROT") {
		t.Fatalf("unknown key error = %v", err)
	}

	writeConfigFile(t, dir, CoreConfigFile, "version: 1\nPORT: many\n")
	if _, err := LoadDir(dir); err == nil || !strings.Contains(err.Error(), "PORT") {
		t.Fatalf("invalid value error = %v", err)
	}
}

func TestLoadDirEnvironmentCannotInstallExtension(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, CoreConfigFile, "version: 1\nPORT: 12346\n")
	t.Setenv("REDIS_ENABLED", "true")
	t.Setenv("REPLAY_ENABLED", "true")

	set, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if set.Config.Redis != nil || set.Config.EffectiveReplayEnabled() {
		t.Fatalf("environment installed absent extension: %+v", set.Config)
	}
}

func TestEnsureConfigDirCreatesOnlyCore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	created, err := EnsureConfigDir(dir)
	if err != nil || !created {
		t.Fatalf("EnsureConfigDir = %v, %v", created, err)
	}
	if !ConfigDirExists(dir) {
		t.Fatal("server.yaml was not created")
	}
	for _, name := range extensionConfigFiles {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("optional file %s should not be created", name)
		}
	}
}

func TestConfigExamplesLoad(t *testing.T) {
	exampleDir := filepath.Join("..", "..", "config.example")
	set, err := LoadDir(exampleDir)
	if err != nil {
		t.Fatalf("config.example must remain loadable: %v", err)
	}
	for _, name := range []string{CoreConfigFile, "network.yaml", "replay.yaml", "redis.yaml"} {
		if !set.HasFile(name) {
			t.Errorf("example is missing %s", name)
		}
	}
	if _, err := LoadAgentFile(filepath.Join(exampleDir, "agent.yaml")); err != nil {
		t.Fatalf("agent.yaml must remain loadable: %v", err)
	}
}

func TestConfigExamplesMentionEverySupportedKey(t *testing.T) {
	exampleDir := filepath.Join("..", "..", "config.example")
	for name, keys := range configFileKeys {
		path := filepath.Join(exampleDir, name)
		if name == "webhook.yaml" || name == "stats.yaml" {
			path = filepath.Join(exampleDir, "legacy", name)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(raw)
		for _, key := range keys {
			if !strings.Contains(text, key+":") {
				t.Errorf("%s does not document supported key %s", name, key)
			}
		}
	}
}

func TestLoadDirRejectsInvalidNestedConfiguration(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, CoreConfigFile, "version: 1\n")
	writeConfigFile(t, dir, "redis.yaml", "version: 1\nPORT: nope\n")
	if _, err := LoadDir(dir); err == nil || !strings.Contains(err.Error(), "PORT") {
		t.Fatalf("invalid Redis error = %v", err)
	}

	if err := os.Remove(filepath.Join(dir, "redis.yaml")); err != nil {
		t.Fatal(err)
	}
	writeConfigFile(t, dir, "webhook.yaml", "version: 1\nTARGETS:\n  - TYPE: generic\n    URl: typo\n")
	if _, err := LoadDir(dir); err == nil || !strings.Contains(err.Error(), "URl") {
		t.Fatalf("invalid webhook error = %v", err)
	}
}

func TestLoadDirRejectsInvalidEnvironmentOverride(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, CoreConfigFile, "version: 1\n")
	t.Setenv("PORT", "nope")
	if _, err := LoadDir(dir); err == nil || !strings.Contains(err.Error(), "PORT") {
		t.Fatalf("invalid environment error = %v", err)
	}
}
