package config

import (
	"reflect"
	"testing"
)

// TestEffectiveDefaults 是本阶段的核心：验证「未设置」时所有默认值正确落地
// （Go 零值陷阱的针对性测试）。
func TestEffectiveDefaults(t *testing.T) {
	c := &ServerConfig{} // 全部未设置
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"ServerName", c.EffectiveServerName(), "Phira MP"},
		{"RoomMaxUsers", c.EffectiveRoomMaxUsers(), 512},
		{"RoomCreationEnabled", c.EffectiveRoomCreationEnabled(), true},
		{"PlayingReconnectGrace", c.EffectivePlayingReconnectGrace(), 5},
		{"MaxRooms", c.EffectiveMaxRooms(), 0},
		{"MaxConnections", c.EffectiveMaxConnections(), 0},
		{"ConnectionRateLimit", c.EffectiveConnectionRateLimit(), 30},
		{"CommandRateLimit", c.EffectiveCommandRateLimit(), true},
		{"HTTPRateLimitMaxRequests", c.EffectiveHTTPRateLimitMaxRequests(), 100},
		{"HTTPRateLimitWindowMS", c.EffectiveHTTPRateLimitWindowMS(), 60000},
		{"ChatEnabled", c.EffectiveChatEnabled(), true},
		{"ReplayEnabled", c.EffectiveReplayEnabled(), false},
		{"ReplayTTLDays", c.EffectiveReplayTTLDays(), 4},
		{"SystemUserID", c.EffectiveSystemUserID(), 0},
		{"RoomListTip", c.EffectiveRoomListTip(), ""},
		{"LogLevel", c.EffectiveLogLevel(), "INFO"},
		{"LogCompressAfterDays", c.EffectiveLogCompressAfterDays(), 14},
		{"LogMaxTotalMB", c.EffectiveLogMaxTotalMB(), 500},
		{"Lang", c.EffectiveLang(), ""},
		{"HAProxyProtocol", c.EffectiveHAProxyProtocol(), false},
		{"Monitors", c.EffectiveMonitors(), []int{2}},
		{"TestAccountIDs", c.EffectiveTestAccountIDs(), []int{1739989}},
		{"CorsOrigins", c.EffectiveCorsOrigins(), []string{}},
		{"AgentIPCEndpoint", c.EffectiveAgentIPC().Endpoint, "disabled"},
		{"AgentWebhookOwner", c.EffectiveAgentIPC().WebhookOwner, "agent"},
	}
	for _, ck := range checks {
		if !reflect.DeepEqual(ck.got, ck.want) {
			t.Errorf("%s default = %v, want %v", ck.name, ck.got, ck.want)
		}
	}
}

// TestExplicitOverridesDefault 验证显式设置覆盖默认值（含显式设为「与默认相同的零」）。
func TestExplicitOverridesDefault(t *testing.T) {
	chat := false
	mu := 16
	c := &ServerConfig{ChatEnabled: &chat, RoomMaxUsers: &mu}
	if c.EffectiveChatEnabled() != false {
		t.Error("explicit chat_enabled=false should stay false, not default true")
	}
	if c.EffectiveRoomMaxUsers() != 16 {
		t.Error("explicit room_max_users=16 should win")
	}
}

func TestBuildFromMap_ParseAndValidate(t *testing.T) {
	m := map[string]any{
		"ROOM_MAX_USERS":  32777, // 超上限 → 钳到 32767
		"REPLAY_TTL_DAYS": 0,     // 非法（<1）→ 忽略，回退默认 4
		"SYSTEM_USER_ID":  12345678,
		"CHAT_ENABLED":    "off",
		"PORT":            "8080",
		"MONITORS":        "1,2,3",
		"OUTBOUND_PROXY":  false,
		"REDIS":           map[string]any{"ENABLED": true, "PORT": 6380},
	}
	c := BuildFromMap(m)
	if c.EffectiveRoomMaxUsers() != 32767 {
		t.Errorf("ROOM_MAX_USERS 32777 should clamp to 32767, got %d", c.EffectiveRoomMaxUsers())
	}
	if c.ReplayTTLDays != nil {
		t.Error("invalid REPLAY_TTL_DAYS should be left unset")
	}
	if c.EffectiveReplayTTLDays() != 4 {
		t.Error("REPLAY_TTL_DAYS should fall back to default 4")
	}
	if c.EffectiveSystemUserID() != 12345678 {
		t.Errorf("SYSTEM_USER_ID should parse to 12345678, got %d", c.EffectiveSystemUserID())
	}
	if c.EffectiveChatEnabled() != false {
		t.Error("CHAT_ENABLED 'off' should parse to false")
	}
	if c.Port == nil || *c.Port != 8080 {
		t.Errorf("PORT '8080' should parse to 8080, got %v", c.Port)
	}
	if !reflect.DeepEqual(c.Monitors, []int{1, 2, 3}) {
		t.Errorf("MONITORS '1,2,3' = %v", c.Monitors)
	}
	if c.OutboundProxy == nil || !c.OutboundProxy.Direct {
		t.Error("OUTBOUND_PROXY false should be Direct")
	}
	if c.Redis == nil || !c.Redis.Enabled || c.Redis.Port != 6380 || c.Redis.Host != "127.0.0.1" {
		t.Errorf("REDIS parse = %+v", c.Redis)
	}
}

func TestBuildFromMap_MonitorsDefault(t *testing.T) {
	c := BuildFromMap(map[string]any{})
	if !reflect.DeepEqual(c.Monitors, []int{2}) {
		t.Errorf("empty map should default Monitors to [2], got %v", c.Monitors)
	}
}

func TestMerge(t *testing.T) {
	mu8 := 8
	baseTip := "base"
	base := &ServerConfig{RoomMaxUsers: &mu8, RoomListTip: &baseTip}
	mu16 := 16
	override := &ServerConfig{RoomMaxUsers: &mu16}
	merged := Merge(base, override)
	if merged.RoomMaxUsers == nil || *merged.RoomMaxUsers != 16 {
		t.Error("override room_max_users should win")
	}
	if merged.RoomListTip == nil || *merged.RoomListTip != "base" {
		t.Error("base room_list_tip should be kept when override absent")
	}
	if !reflect.DeepEqual(merged.Monitors, []int{2}) {
		t.Error("merge should bake monitors default [2]")
	}
	if !reflect.DeepEqual(merged.TestAccountIDs, []int{1739989}) {
		t.Error("merge should bake test_account_ids default")
	}
}

func TestMergeAgentIPCFields(t *testing.T) {
	base := &ServerConfig{AgentIPC: &AgentIPCConfig{Endpoint: "auto", DiscoveryFile: "server.json", Instance: "blue", OutboxDir: "queue", OutboxMaxMB: 32, WebhookOwner: "agent"}}
	override := &ServerConfig{AgentIPC: &AgentIPCConfig{Token: "environment-secret"}}
	merged := Merge(base, override).EffectiveAgentIPC()
	if merged.Endpoint != "auto" || merged.DiscoveryFile != "server.json" || merged.Instance != "blue" || merged.Token != "environment-secret" || merged.OutboxDir != "queue" || merged.OutboxMaxMB != 32 || merged.WebhookOwner != "agent" {
		t.Fatalf("Agent IPC fields were not merged independently: %+v", merged)
	}
}

func TestLoadEnvAgentIPCTokenOnly(t *testing.T) {
	t.Setenv("AGENT_IPC_TOKEN", "environment-secret")
	cfg := LoadEnv()
	if cfg.AgentIPC == nil || cfg.AgentIPC.Token != "environment-secret" || cfg.AgentIPC.Endpoint != "" {
		t.Fatalf("token-only Agent IPC environment override = %+v", cfg.AgentIPC)
	}
}

func TestChangedKeys(t *testing.T) {
	a := BuildFromMap(map[string]any{"ROOM_MAX_USERS": 8, "CHAT_ENABLED": true})
	b := BuildFromMap(map[string]any{"ROOM_MAX_USERS": 16, "CHAT_ENABLED": true})
	changed := ChangedKeys(a, b)
	found := false
	for _, k := range changed {
		if k == "ROOM_MAX_USERS" {
			found = true
		}
		if k == "CHAT_ENABLED" {
			t.Error("CHAT_ENABLED unchanged should not appear in diff")
		}
	}
	if !found {
		t.Errorf("ROOM_MAX_USERS change not detected; changed=%v", changed)
	}
}

func TestKeepStartupOnly(t *testing.T) {
	host1 := "127.0.0.1"
	host2 := "0.0.0.0"
	mu16 := 16
	prev := &ServerConfig{Host: &host1}
	next := &ServerConfig{Host: &host2, RoomMaxUsers: &mu16} // HOST 仅启动期生效。
	cfg, restart := KeepStartupOnly(prev, next)
	if cfg.Host == nil || *cfg.Host != "127.0.0.1" {
		t.Errorf("startup-only HOST should revert to prev, got %v", cfg.Host)
	}
	if cfg.RoomMaxUsers == nil || *cfg.RoomMaxUsers != 16 {
		t.Error("non-startup-only ROOM_MAX_USERS should keep next value")
	}
	if len(restart) != 1 || restart[0] != "HOST" {
		t.Errorf("restart keys = %v, want [HOST]", restart)
	}
}

func TestParseBoolValue(t *testing.T) {
	truthy := []any{true, 1, "1", "true", "YES", "on", " On "}
	for _, v := range truthy {
		if b, ok := parseBoolValue(v); !ok || !b {
			t.Errorf("parseBoolValue(%v) should be true", v)
		}
	}
	falsy := []any{false, 0, "0", "false", "no", "OFF"}
	for _, v := range falsy {
		if b, ok := parseBoolValue(v); !ok || b {
			t.Errorf("parseBoolValue(%v) should be false", v)
		}
	}
	if _, ok := parseBoolValue("maybe"); ok {
		t.Error("parseBoolValue('maybe') should be invalid")
	}
}

func TestParseOutboundProxy(t *testing.T) {
	if p, ok := parseOutboundProxyValue(false); !ok || !p.Direct {
		t.Error("false → Direct")
	}
	if p, ok := parseOutboundProxyValue("http://proxy:8080"); !ok || p.URL != "http://proxy:8080" || p.Direct {
		t.Error("url → URL")
	}
	if p, ok := parseOutboundProxyValue("FALSE"); !ok || !p.Direct {
		t.Error("'FALSE' string → Direct")
	}
	if _, ok := parseOutboundProxyValue(true); ok {
		t.Error("true is invalid for outbound proxy")
	}
}

func TestStartupOnlyEnvNames(t *testing.T) {
	names := StartupOnlyEnvNames()
	want := map[string]bool{"HOST": true, "PORT": true, "HTTP_SERVICE": true, "HTTP_PORT": true, "GUI": true, "ADMIN_DATA_PATH": true, "REDIS": true, "STATS_DB_PATH": true, "AGENT_IPC": true}
	for _, n := range names {
		if !want[n] {
			t.Errorf("unexpected startup-only key %q", n)
		}
		delete(want, n)
	}
	if len(want) != 0 {
		t.Errorf("missing startup-only keys: %v", want)
	}
}

func TestEffectiveDNSServers_Default(t *testing.T) {
	c := &ServerConfig{}
	if !reflect.DeepEqual(c.EffectiveDNSServers(), DefaultDNSServers) {
		t.Errorf("default DNS servers = %v, want %v", c.EffectiveDNSServers(), DefaultDNSServers)
	}
}

func TestEffectiveDNSServers_Custom(t *testing.T) {
	c := &ServerConfig{Netutil: &NetutilConfig{DNSServers: []string{"9.9.9.9:53", "149.112.112.112:53"}}}
	want := []string{"9.9.9.9:53", "149.112.112.112:53"}
	if !reflect.DeepEqual(c.EffectiveDNSServers(), want) {
		t.Errorf("custom DNS servers = %v, want %v", c.EffectiveDNSServers(), want)
	}
}

func TestEffectiveDNSServers_EmptyFallback(t *testing.T) {
	c := &ServerConfig{Netutil: &NetutilConfig{DNSServers: []string{}}}
	if !reflect.DeepEqual(c.EffectiveDNSServers(), DefaultDNSServers) {
		t.Errorf("empty DNS servers should fall back to default, got %v", c.EffectiveDNSServers())
	}
}

func TestEffectiveDNSServers_TrimWhitespace(t *testing.T) {
	c := &ServerConfig{Netutil: &NetutilConfig{DNSServers: []string{"  9.9.9.9:53  ", "", "  ", "149.112.112.112:53"}}}
	want := []string{"9.9.9.9:53", "149.112.112.112:53"}
	if !reflect.DeepEqual(c.EffectiveDNSServers(), want) {
		t.Errorf("trimmed DNS servers = %v, want %v", c.EffectiveDNSServers(), want)
	}
}

func TestParseNetutilValue(t *testing.T) {
	if _, ok := parseNetutilValue("not a map"); ok {
		t.Error("parseNetutilValue should reject non-map")
	}

	cfg, ok := parseNetutilValue(map[string]any{"DNS_SERVERS": []any{"9.9.9.9:53", "149.112.112.112:53"}})
	if !ok {
		t.Fatal("parseNetutilValue should accept valid map")
	}
	want := []string{"9.9.9.9:53", "149.112.112.112:53"}
	if !reflect.DeepEqual(cfg.DNSServers, want) {
		t.Errorf("parsed DNS servers = %v, want %v", cfg.DNSServers, want)
	}

	cfg, ok = parseNetutilValue(map[string]any{"DNS_SERVERS": "1.1.1.1:53, 8.8.8.8:53"})
	if !ok {
		t.Fatal("parseNetutilValue should accept string list")
	}
	want = []string{"1.1.1.1:53", "8.8.8.8:53"}
	if !reflect.DeepEqual(cfg.DNSServers, want) {
		t.Errorf("parsed DNS servers from string = %v, want %v", cfg.DNSServers, want)
	}

	cfg, ok = parseNetutilValue(map[string]any{})
	if !ok {
		t.Fatal("parseNetutilValue should accept empty map")
	}
	if cfg.DNSServers != nil {
		t.Error("empty map should produce nil DNSServers")
	}
}

func TestBuildFromMap_NETUTIL(t *testing.T) {
	c := BuildFromMap(map[string]any{
		"NETUTIL": map[string]any{"DNS_SERVERS": []string{"9.9.9.9:53"}},
	})
	if c.Netutil == nil {
		t.Fatal("NETUTIL should be parsed")
	}
	if !reflect.DeepEqual(c.EffectiveDNSServers(), []string{"9.9.9.9:53"}) {
		t.Errorf("NETUTIL DNS servers = %v", c.EffectiveDNSServers())
	}
}

func TestParseWebhookFeishuTemplateVersions(t *testing.T) {
	cfg, ok := parseWebhookValue(map[string]any{
		"ENABLED": true,
		"TARGETS": []any{map[string]any{
			"TYPE":                      "feishu",
			"APP_ID":                    "cli_test",
			"APP_SECRET":                "secret",
			"RECEIVE_OPEN_ID":           "ou_test",
			"TEMPLATE_ID":               "start-template",
			"TEMPLATE_VERSION":          "2.3.4",
			"GAME_END_TEMPLATE_ID":      "end-template",
			"GAME_END_TEMPLATE_VERSION": "5.6.7",
			"LIVE_UPDATE":               true,
		}},
	})
	if !ok || len(cfg.Targets) != 1 {
		t.Fatalf("unexpected webhook config: ok=%v cfg=%+v", ok, cfg)
	}
	target := cfg.Targets[0]
	if target.TemplateVersion != "2.3.4" || target.GameEndTemplateVersion != "5.6.7" {
		t.Fatalf("template versions were not preserved: %+v", target)
	}
	if !target.LiveUpdate {
		t.Fatal("LIVE_UPDATE was not parsed")
	}
}

func TestParseWebhookOneBotV11(t *testing.T) {
	cfg, ok := parseWebhookValue(map[string]any{
		"ENABLED": true,
		"TARGETS": []any{
			map[string]any{
				"TYPE": "onebot-v11", "URL": "http://127.0.0.1:5700/", "ACCESS_TOKEN": "token",
				"MESSAGE_TYPE": "GROUP", "TARGET_ID": "123456", "EVENTS": []any{"game_start"},
			},
			map[string]any{"TYPE": "onebot_v11", "URL": "http://127.0.0.1:5700", "MESSAGE_TYPE": "invalid", "TARGET_ID": 1},
		},
	})
	if !ok || len(cfg.Targets) != 1 {
		t.Fatalf("unexpected webhook config: ok=%v cfg=%+v", ok, cfg)
	}
	target := cfg.Targets[0]
	if target.Type != "onebot_v11" || target.AccessToken != "token" || target.MessageType != "group" || target.TargetID != 123456 {
		t.Fatalf("unexpected OneBot target: %+v", target)
	}
	if !target.Subscribes("game_start") || target.Subscribes("game_end") {
		t.Fatalf("OneBot event subscriptions were not preserved: %+v", target.Events)
	}
}

func TestParseWebhookOneBotV11TargetIDArray(t *testing.T) {
	cfg, ok := parseWebhookValue(map[string]any{
		"ENABLED": true,
		"TARGETS": []any{map[string]any{
			"TYPE": "onebot_v11", "URL": "http://127.0.0.1:5700",
			"MESSAGE_TYPE": "group", "TARGET_ID": []any{123456, "654321"},
		}},
	})
	if !ok || len(cfg.Targets) != 1 {
		t.Fatalf("unexpected webhook config: ok=%v cfg=%+v", ok, cfg)
	}
	target := cfg.Targets[0]
	if target.TargetID != 123456 || len(target.TargetIDs) != 2 || target.TargetIDs[0] != 123456 || target.TargetIDs[1] != 654321 {
		t.Fatalf("unexpected OneBot target IDs: %+v", target)
	}
}

func TestWebhookTargetScoreSubmittedIsFeishuLiveOnly(t *testing.T) {
	cases := []struct {
		name   string
		target WebhookTarget
		want   bool
	}{
		{name: "generic subscribe all", target: WebhookTarget{Type: "generic"}, want: false},
		{name: "discord explicit", target: WebhookTarget{Type: "discord", Events: []string{"score_submitted"}}, want: false},
		{name: "feishu disabled", target: WebhookTarget{Type: "feishu"}, want: false},
		{name: "feishu live", target: WebhookTarget{Type: "feishu", LiveUpdate: true}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.target.Subscribes("score_submitted"); got != tc.want {
				t.Fatalf("Subscribes(score_submitted)=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestWebhookTargetLiveUpdateReceivesCleanup(t *testing.T) {
	target := WebhookTarget{Type: "feishu", LiveUpdate: true, Events: []string{"game_start", "game_end"}}
	if !target.Subscribes("game_end") {
		t.Fatal("live-update target must receive game_end for its final update")
	}
	if !target.Subscribes("room_disband") {
		t.Fatal("live-update target must receive room_disband for cleanup")
	}
}

func TestLoadEnv_NETUTIL_DNS_SERVERS(t *testing.T) {
	t.Setenv("NETUTIL_DNS_SERVERS", "9.9.9.9:53,149.112.112.112:53")
	c := LoadEnv()
	if c.Netutil == nil {
		t.Fatal("NETUTIL should be set from env")
	}
	want := []string{"9.9.9.9:53", "149.112.112.112:53"}
	if !reflect.DeepEqual(c.EffectiveDNSServers(), want) {
		t.Errorf("env DNS servers = %v, want %v", c.EffectiveDNSServers(), want)
	}
}
