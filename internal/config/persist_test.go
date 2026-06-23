package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyConfigUpdates_ReplaceInPlacePreservesComments(t *testing.T) {
	src := "# 顶部注释\nREPLAY_ENABLED: false  # 行尾注释\nPORT: 12346\n\n# 末尾注释\n"
	out := ApplyConfigUpdates(src, map[string]any{"REPLAY_ENABLED": true})
	if !strings.Contains(out, "REPLAY_ENABLED: true") {
		t.Errorf("value not updated:\n%s", out)
	}
	if !strings.Contains(out, "# 顶部注释") || !strings.Contains(out, "# 末尾注释") {
		t.Errorf("comments must be preserved:\n%s", out)
	}
	if !strings.Contains(out, "PORT: 12346") {
		t.Errorf("other keys must be preserved:\n%s", out)
	}
	// 不应重复追加该键。
	if strings.Count(out, "REPLAY_ENABLED:") != 1 {
		t.Errorf("REPLAY_ENABLED should appear once, got:\n%s", out)
	}
}

func TestApplyConfigUpdates_AppendsMissingKey(t *testing.T) {
	out := ApplyConfigUpdates("PORT: 12346\n", map[string]any{"CHAT_ENABLED": false})
	if !strings.Contains(out, "CHAT_ENABLED: false") {
		t.Errorf("missing key should be appended:\n%s", out)
	}
}

func TestApplyConfigUpdates_DoesNotTouchNestedSameNameKey(t *testing.T) {
	// 嵌套块里的同名子键（有缩进）不能被顶层匹配误伤。
	src := "REDIS:\n  ENABLED: true\nENABLED: false\n"
	out := ApplyConfigUpdates(src, map[string]any{"ENABLED": true})
	if !strings.Contains(out, "  ENABLED: true") {
		t.Errorf("nested ENABLED must stay untouched:\n%s", out)
	}
	// 顶层 ENABLED 被改为 true。
	lines := strings.Split(out, "\n")
	var topFound bool
	for _, ln := range lines {
		if ln == "ENABLED: true" {
			topFound = true
		}
	}
	if !topFound {
		t.Errorf("top-level ENABLED should be updated to true:\n%s", out)
	}
}

func TestApplyConfigUpdates_CommentedExampleNotMatched(t *testing.T) {
	// 被注释掉的示例行不算命中，应在末尾追加活动行。
	src := "# REPLAY_ENABLED: false\n"
	out := ApplyConfigUpdates(src, map[string]any{"REPLAY_ENABLED": true})
	if !strings.Contains(out, "# REPLAY_ENABLED: false") {
		t.Errorf("commented example must be preserved:\n%s", out)
	}
	if !strings.Contains(out, "\nREPLAY_ENABLED: true\n") {
		t.Errorf("active line should be appended:\n%s", out)
	}
}

func TestApplyConfigUpdates_StringQuoted(t *testing.T) {
	out := ApplyConfigUpdates("", map[string]any{"ROOM_LIST_TIP": "yes"})
	// 字符串应加引号，避免 yes 被 YAML 解析为布尔。
	if !strings.Contains(out, `ROOM_LIST_TIP: "yes"`) {
		t.Errorf("string should be quoted:\n%s", out)
	}
}

func TestPersistConfigValues_Roundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server_config.yml")
	if err := os.WriteFile(path, []byte("# cfg\nREPLAY_ENABLED: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := PersistConfigValues(path, map[string]any{"REPLAY_ENABLED": true, "MAX_ROOMS": 5}); err != nil {
		t.Fatalf("persist: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if !strings.Contains(got, "REPLAY_ENABLED: true") {
		t.Errorf("REPLAY_ENABLED not persisted:\n%s", got)
	}
	if !strings.Contains(got, "MAX_ROOMS: 5") {
		t.Errorf("MAX_ROOMS not appended:\n%s", got)
	}
	if !strings.Contains(got, "# cfg") {
		t.Errorf("comment lost:\n%s", got)
	}
}

func TestPersistConfigValues_MissingFileCreates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "new_config.yml")
	if err := PersistConfigValues(path, map[string]any{"CHAT_ENABLED": false}); err != nil {
		t.Fatalf("persist to missing file: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file should be created: %v", err)
	}
	if !strings.Contains(string(raw), "CHAT_ENABLED: false") {
		t.Errorf("value not written:\n%s", raw)
	}
}
