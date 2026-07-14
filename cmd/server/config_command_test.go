package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunConfigCommandDryRun(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "server_config.yml")
	if err := os.WriteFile(legacy, []byte("PORT: 12346\nREPLAY_ENABLED: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	handled, code := runConfigCommand([]string{
		"config", "migrate", "-from", legacy, "-to", filepath.Join(dir, "config"), "--dry-run",
	}, &stdout, &stderr)
	if !handled || code != 0 {
		t.Fatalf("handled=%v code=%d stderr=%s", handled, code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "server.yaml") || !strings.Contains(stdout.String(), "replay.yaml") {
		t.Fatalf("unexpected plan:\n%s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "config")); !os.IsNotExist(err) {
		t.Fatal("dry run wrote destination")
	}
}
