package l10n

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOverrides(t *testing.T) {
	ensureBundles()
	const key = "log-server-name"
	// 保存并在结束时恢复，避免污染其它测试的全局 bundles。
	orig, had := bundles["zh-CN"][key]
	defer func() {
		if had {
			bundles["zh-CN"][key] = orig
		} else {
			delete(bundles["zh-CN"], key)
		}
	}()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "zh-CN.ftl"), []byte("log-server-name = 自定义 { $name }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := LoadOverrides(dir); n != 1 {
		t.Fatalf("expected 1 language overridden, got %d", n)
	}
	got := TL(NewLanguage("zh-CN"), key, map[string]string{"name": "X"})
	if got != "自定义 X" {
		t.Errorf("override not applied: got %q", got)
	}
	// 未覆盖的键应保持内置翻译。
	if other := TL(NewLanguage("zh-CN"), "net-connection-closed", nil); other == "" || other == "net-connection-closed" {
		t.Errorf("non-overridden key should keep builtin translation, got %q", other)
	}
}

func TestLoadOverrides_MissingDirNoop(t *testing.T) {
	if n := LoadOverrides(filepath.Join(t.TempDir(), "nonexistent")); n != 0 {
		t.Errorf("missing dir should override nothing, got %d", n)
	}
	if n := LoadOverrides(""); n != 0 {
		t.Errorf("empty dir should be a no-op, got %d", n)
	}
}
