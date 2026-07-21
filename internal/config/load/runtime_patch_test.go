package load

import (
	"reflect"
	"slices"
	"testing"
)

func TestRuntimeSnapshot_Defaults(t *testing.T) {
	snap := BuildRuntimeConfigSnapshot(&ServerConfig{})
	// 抽查若干默认值（含默认应用）。
	cases := map[string]any{
		"ROOM_CREATION_ENABLED":        true,
		"REPLAY_ENABLED":               false,
		"ROOM_MAX_USERS":               DefaultRoomMaxUsers,
		"MAX_ROOMS":                    0,
		"CONNECTION_RATE_LIMIT":        DefaultConnectionRateLimit,
		"COMMAND_RATE_LIMIT":           true,
		"HTTP_RATE_LIMIT_MAX_REQUESTS": 100,
		"CHAT_ENABLED":                 true,
		"LOG_LEVEL":                    DefaultLogLevel,
		"ROOM_LIST_TIP":                "",
	}
	for k, want := range cases {
		if got := snap[k]; !reflect.DeepEqual(got, want) {
			t.Errorf("snapshot[%s] = %v (%T), want %v (%T)", k, got, got, want, want)
		}
	}
	// 快照应覆盖全部声明的运行时键。
	if len(snap) != len(RuntimeConfigEnvNames()) {
		t.Errorf("snapshot has %d keys, want %d", len(snap), len(RuntimeConfigEnvNames()))
	}
}

func TestRuntimePatch_ApplyScalars(t *testing.T) {
	cfg := &ServerConfig{}
	res := ParseRuntimeConfigPatch(map[string]any{
		"REPLAY_ENABLED": true,
		"ROOM_MAX_USERS": float64(6), // 模拟 JSON 数字
		"LOG_LEVEL":      "debug",    // 大小写不敏感
	})
	if !res.OK {
		t.Fatalf("expected ok, got invalid=%v unsupported=%v", res.InvalidKeys, res.UnsupportedKeys)
	}
	res.Apply(cfg)
	if !cfg.EffectiveReplayEnabled() {
		t.Error("REPLAY_ENABLED not applied")
	}
	if cfg.EffectiveRoomMaxUsers() != 6 {
		t.Errorf("ROOM_MAX_USERS = %d, want 6", cfg.EffectiveRoomMaxUsers())
	}
	if cfg.EffectiveLogLevel() != "DEBU" {
		t.Errorf("LOG_LEVEL = %s, want DEBU", cfg.EffectiveLogLevel())
	}
	// Persist 应保留解析后的标量值（用于落盘）。
	if res.Persist["LOG_LEVEL"] != "DEBU" {
		t.Errorf("persist LOG_LEVEL = %v, want DEBU", res.Persist["LOG_LEVEL"])
	}
}

func TestRuntimePatch_MaxRoomsZeroClears(t *testing.T) {
	cfg := &ServerConfig{}
	// 先设一个非零值。
	ParseAndApply(t, cfg, map[string]any{"MAX_ROOMS": 10})
	if cfg.EffectiveMaxRooms() != 10 {
		t.Fatalf("setup: MAX_ROOMS = %d, want 10", cfg.EffectiveMaxRooms())
	}
	// 设 0 → 视为未设置（无限）。
	ParseAndApply(t, cfg, map[string]any{"MAX_ROOMS": 0})
	if cfg.MaxRooms != nil {
		t.Errorf("MAX_ROOMS=0 should clear the field (unlimited), got %v", *cfg.MaxRooms)
	}
}

func TestRuntimePatch_RoomListTipTrimAndClear(t *testing.T) {
	cfg := &ServerConfig{}
	ParseAndApply(t, cfg, map[string]any{"ROOM_LIST_TIP": "  hello  "})
	if cfg.EffectiveRoomListTip() != "hello" {
		t.Errorf("ROOM_LIST_TIP = %q, want hello", cfg.EffectiveRoomListTip())
	}
	// 空白字符串 → 清除。
	ParseAndApply(t, cfg, map[string]any{"ROOM_LIST_TIP": "   "})
	if cfg.RoomListTip != nil {
		t.Errorf("blank tip should clear, got %q", *cfg.RoomListTip)
	}
	// null → 清除。
	ParseAndApply(t, cfg, map[string]any{"ROOM_LIST_TIP": "x"})
	ParseAndApply(t, cfg, map[string]any{"ROOM_LIST_TIP": nil})
	if cfg.RoomListTip != nil {
		t.Errorf("null tip should clear, got %q", *cfg.RoomListTip)
	}
}

func TestRuntimePatch_Classification(t *testing.T) {
	res := ParseRuntimeConfigPatch(map[string]any{
		"PORT":           12346,   // 仅启动期生效。
		"REPLAY_ENABLED": "maybe", // 值非法
		"NOT_A_KEY":      1,       // 未知
		"REAL_IP_HEADER": "X-Foo", // 已知但非运行时项 → unsupported
	})
	if res.OK {
		t.Fatal("expected not-ok (no applicable keys)")
	}
	if !res.Empty {
		t.Error("expected Empty=true")
	}
	if !contains(res.StartupOnlyKeys, "PORT") {
		t.Errorf("PORT should be startup-only, got %v", res.StartupOnlyKeys)
	}
	if !contains(res.InvalidKeys, "REPLAY_ENABLED") {
		t.Errorf("REPLAY_ENABLED should be invalid, got %v", res.InvalidKeys)
	}
	if !contains(res.UnsupportedKeys, "NOT_A_KEY") || !contains(res.UnsupportedKeys, "REAL_IP_HEADER") {
		t.Errorf("NOT_A_KEY and REAL_IP_HEADER should be unsupported, got %v", res.UnsupportedKeys)
	}
}

func TestRuntimePatch_PartialValidStillApplies(t *testing.T) {
	// 一个合法 + 一个非法：整体 OK，仅合法键应用；非法键被静默丢弃（对齐 TS：ok 时不回报 invalid）。
	res := ParseRuntimeConfigPatch(map[string]any{
		"CHAT_ENABLED":   false,
		"REPLAY_ENABLED": "garbage",
	})
	if !res.OK {
		t.Fatal("expected ok (one valid key present)")
	}
	if !contains(res.Keys, "CHAT_ENABLED") {
		t.Errorf("CHAT_ENABLED should be in Keys, got %v", res.Keys)
	}
	if contains(res.Keys, "REPLAY_ENABLED") {
		t.Errorf("invalid REPLAY_ENABLED should not be applied, Keys=%v", res.Keys)
	}
}

func TestPickRuntimeConfigSnapshot(t *testing.T) {
	enabled := true
	cfg := &ServerConfig{ReplayEnabled: &enabled}
	snap := PickRuntimeConfigSnapshot(cfg, []string{"replay_enabled", "  log_level  ", "bogus"})
	if snap["REPLAY_ENABLED"] != true {
		t.Errorf("picked REPLAY_ENABLED = %v, want true", snap["REPLAY_ENABLED"])
	}
	if snap["LOG_LEVEL"] != DefaultLogLevel {
		t.Errorf("picked LOG_LEVEL = %v, want %s", snap["LOG_LEVEL"], DefaultLogLevel)
	}
	if _, ok := snap["BOGUS"]; ok {
		t.Error("bogus key should be ignored")
	}
	if len(snap) != 2 {
		t.Errorf("picked %d keys, want 2", len(snap))
	}
}

// ParseAndApply 是测试辅助：解析补丁并断言 OK，然后应用到 cfg。
func ParseAndApply(t *testing.T, cfg *ServerConfig, raw map[string]any) {
	t.Helper()
	res := ParseRuntimeConfigPatch(raw)
	if !res.OK {
		t.Fatalf("patch %v not ok: invalid=%v unsupported=%v", raw, res.InvalidKeys, res.UnsupportedKeys)
	}
	res.Apply(cfg)
}

func contains(s []string, v string) bool {
	return slices.Contains(s, v)
}
