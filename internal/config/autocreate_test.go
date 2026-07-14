package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestEnsureDefaultFile_CopiesLocalExample(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "server_config.yml")
	example := filepath.Join(dir, "server_config.example.yml")
	want := "# example\nPORT: 23456\nMONITORS:\n  - 9\n"
	if err := os.WriteFile(example, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}

	created, err := EnsureDefaultFile(cfgPath)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v, want created=true nil", created, err)
	}
	got, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("config content = %q, want example content %q", got, want)
	}
	// 生成后应能被正常加载，且 CLI 校验值落地（端口 23456、监视者 [9]）。
	cfg, fromFile, err := LoadMerged(cfgPath)
	if err != nil || !fromFile {
		t.Fatalf("LoadMerged fromFile=%v err=%v", fromFile, err)
	}
	if cfg.Port == nil || *cfg.Port != 23456 {
		t.Fatalf("Port = %v, want 23456", cfg.Port)
	}
}

func TestEnsureDefaultFile_FallsBackToBuiltinTemplate(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "server_config.yml") // 同目录无 example

	created, err := EnsureDefaultFile(cfgPath)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v, want created=true nil", created, err)
	}
	got, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != DefaultConfigYAML {
		t.Fatalf("config content = %q, want builtin template", got)
	}
}

func TestDefaultConfigYAMLMatchesRuntimeDefaults(t *testing.T) {
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(DefaultConfigYAML), &raw); err != nil {
		t.Fatalf("parse DefaultConfigYAML: %v", err)
	}
	cfg := BuildFromMap(raw)
	if cfg.EffectiveRoomMaxUsers() != DefaultRoomMaxUsers {
		t.Fatalf("template ROOM_MAX_USERS = %d, runtime default = %d", cfg.EffectiveRoomMaxUsers(), DefaultRoomMaxUsers)
	}
}

func TestEnsureDefaultFile_DoesNotOverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "server_config.yml")
	original := "PORT: 11111\n"
	if err := os.WriteFile(cfgPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	created, err := EnsureDefaultFile(cfgPath)
	if err != nil || created {
		t.Fatalf("created=%v err=%v, want created=false nil (no overwrite)", created, err)
	}
	got, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("existing config was modified: %q", got)
	}
}
