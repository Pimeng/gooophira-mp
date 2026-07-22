package load

import (
	"os"
)

var configFields = []fieldSpec{
	intListField("MONITORS", func(c *ServerConfig) *[]int { return &c.Monitors }),
	intListField("TEST_ACCOUNT_IDS", func(c *ServerConfig) *[]int { return &c.TestAccountIDs }),
	strField("SERVER_NAME", false, parseStringValue, func(c *ServerConfig) **string { return &c.ServerName }),
	strField("HOST", true, parseStringValue, func(c *ServerConfig) **string { return &c.Host }),
	intField("PORT", true, parsePortValue, func(c *ServerConfig) **int { return &c.Port }),
	boolField("HTTP_SERVICE", true, func(c *ServerConfig) **bool { return &c.HTTPService }),
	intField("HTTP_PORT", true, parsePortValue, func(c *ServerConfig) **int { return &c.HTTPPort }),
	intField("ROOM_MAX_USERS", false, parseRoomMaxUsersValue, func(c *ServerConfig) **int { return &c.RoomMaxUsers }),
	boolField("ROOM_CREATION_ENABLED", false, func(c *ServerConfig) **bool { return &c.RoomCreationEnabled }),
	intField("PLAYING_RECONNECT_GRACE", false, parsePlayingGraceValue, func(c *ServerConfig) **int { return &c.PlayingReconnectGrace }),
	intField("MAX_ROOMS", false, parsePositiveIntValue, func(c *ServerConfig) **int { return &c.MaxRooms }),
	intField("MAX_CONNECTIONS", false, parsePositiveIntValue, func(c *ServerConfig) **int { return &c.MaxConnections }),
	intField("CONNECTION_RATE_LIMIT", false, parsePositiveIntValue, func(c *ServerConfig) **int { return &c.ConnectionRateLimit }),
	boolField("COMMAND_RATE_LIMIT", false, func(c *ServerConfig) **bool { return &c.CommandRateLimit }),
	intField("HTTP_RATE_LIMIT_MAX_REQUESTS", false, parsePositiveIntValue, func(c *ServerConfig) **int { return &c.HTTPRateLimitMaxRequests }),
	intField("HTTP_RATE_LIMIT_WINDOW_MS", false, parsePositiveIntValue, func(c *ServerConfig) **int { return &c.HTTPRateLimitWindowMS }),
	boolField("CHAT_ENABLED", false, func(c *ServerConfig) **bool { return &c.ChatEnabled }),
	boolField("REPLAY_ENABLED", false, func(c *ServerConfig) **bool { return &c.ReplayEnabled }),
	strField("REPLAY_BASE_DIR", false, parseStringValue, func(c *ServerConfig) **string { return &c.ReplayBaseDir }),
	intField("REPLAY_TTL_DAYS", false, parseReplayTTLDaysValue, func(c *ServerConfig) **int { return &c.ReplayTTLDays }),
	boolField("REPLAY_AUTO_UPLOAD", false, func(c *ServerConfig) **bool { return &c.ReplayAutoUpload }),
	intField("SYSTEM_USER_ID", false, parseNonNegativeIntValue, func(c *ServerConfig) **int { return &c.SystemUserID }),
	strField("ADMIN_TOKEN", false, parseStringValue, func(c *ServerConfig) **string { return &c.AdminToken }),
	strField("ADMIN_DATA_PATH", true, parseStringValue, func(c *ServerConfig) **string { return &c.AdminDataPath }),
	strField("ROOM_LIST_TIP", false, parseStringValue, func(c *ServerConfig) **string { return &c.RoomListTip }),
	strField("LOG_LEVEL", false, parseLogLevelValue, func(c *ServerConfig) **string { return &c.LogLevel }),
	intField("LOG_COMPRESS_AFTER_DAYS", false, parseNonNegativeIntValue, func(c *ServerConfig) **int { return &c.LogCompressAfterDays }),
	intField("LOG_MAX_TOTAL_MB", false, parseNonNegativeIntValue, func(c *ServerConfig) **int { return &c.LogMaxTotalMB }),
	strField("REAL_IP_HEADER", false, parseStringValue, func(c *ServerConfig) **string { return &c.RealIPHeader }),
	strListField("CORS_ORIGINS", func(c *ServerConfig) *[]string { return &c.CorsOrigins }),
	boolField("HAPROXY_PROTOCOL", false, func(c *ServerConfig) **bool { return &c.HAProxyProtocol }),
	{
		env:   "LANG",
		parse: func(v any) (any, bool) { return parseStringValue(v) },
		get: func(c *ServerConfig) any {
			if c.Lang == nil {
				return nil
			}
			return *c.Lang
		},
		set:   func(c *ServerConfig, v any) { s := v.(string); c.Lang = &s },
		clear: func(c *ServerConfig) { c.Lang = nil },
		// PHIRA_MP_LANG 优先，避免系统 LANG（如 en_US.UTF-8）误读；空视同未设置回退 LANG。
		envInput: func() (any, bool) {
			if v := os.Getenv("PHIRA_MP_LANG"); v != "" {
				return v, true
			}
			if v := os.Getenv("LANG"); v != "" {
				return v, true
			}
			return nil, false
		},
	},
	strField("PHIRA_API_ENDPOINT", false, parseStringValue, func(c *ServerConfig) **string { return &c.PhiraAPIEndpoint }),
	{
		env:   "OUTBOUND_PROXY",
		parse: func(v any) (any, bool) { return parseOutboundProxyValue(v) },
		get: func(c *ServerConfig) any {
			if c.OutboundProxy == nil {
				return nil
			}
			return *c.OutboundProxy
		},
		set:   func(c *ServerConfig, v any) { p := v.(*OutboundProxy); c.OutboundProxy = p },
		clear: func(c *ServerConfig) { c.OutboundProxy = nil },
	},
	{
		env:   "NETUTIL",
		parse: func(v any) (any, bool) { return parseNetutilValue(v) },
		get: func(c *ServerConfig) any {
			if c.Netutil == nil {
				return nil
			}
			return *c.Netutil
		},
		set:   func(c *ServerConfig, v any) { c.Netutil = v.(*NetutilConfig) },
		clear: func(c *ServerConfig) { c.Netutil = nil },
		envInput: func() (any, bool) {
			v := os.Getenv("NETUTIL_DNS_SERVERS")
			if v == "" {
				return nil, false
			}
			return map[string]any{"DNS_SERVERS": v}, true
		},
	},
	{
		env:   "SHARE_STATION",
		parse: func(v any) (any, bool) { return parseShareStationValue(v) },
		get: func(c *ServerConfig) any {
			if c.ShareStation == nil {
				return nil
			}
			return *c.ShareStation
		},
		set:   func(c *ServerConfig, v any) { c.ShareStation = v.(*ShareStation) },
		clear: func(c *ServerConfig) { c.ShareStation = nil },
		envInput: func() (any, bool) {
			url, token := os.Getenv("SHARE_STATION_URL"), os.Getenv("SHARE_STATION_TOKEN")
			if url == "" && token == "" {
				return nil, false
			}
			return map[string]any{"URL": url, "TOKEN": token}, true
		},
	},
	{
		env: "REDIS", startupOnly: true,
		parse: func(v any) (any, bool) { return parseRedisValue(v) },
		get: func(c *ServerConfig) any {
			if c.Redis == nil {
				return nil
			}
			return *c.Redis
		},
		set:   func(c *ServerConfig, v any) { c.Redis = v.(*RedisConfig) },
		clear: func(c *ServerConfig) { c.Redis = nil },
		envInput: func() (any, bool) {
			enabled := os.Getenv("REDIS_ENABLED")
			if enabled == "" {
				return nil, false
			}
			return map[string]any{
				"ENABLED":  enabled,
				"HOST":     os.Getenv("REDIS_HOST"),
				"PORT":     os.Getenv("REDIS_PORT"),
				"PASSWORD": os.Getenv("REDIS_PASSWORD"),
				"DB":       os.Getenv("REDIS_DB"),
			}, true
		},
	},
	strField("HITOKOTO_API_URL", false, parseStringValue, func(c *ServerConfig) **string { return &c.HitokotoAPIURL }),
	boolField("ALLOW_TOKEN_IN_QUERY", false, func(c *ServerConfig) **bool { return &c.AllowTokenInQuery }),
	{
		// WEBHOOK：嵌套含目标列表，仅经 YAML 配置（无 env 合成）；并非仅启动期配置，支持热重载。
		env:   "WEBHOOK",
		parse: func(v any) (any, bool) { return parseWebhookValue(v) },
		get: func(c *ServerConfig) any {
			if c.Webhook == nil {
				return nil
			}
			return *c.Webhook
		},
		set:   func(c *ServerConfig, v any) { c.Webhook = v.(*WebhookConfig) },
		clear: func(c *ServerConfig) { c.Webhook = nil },
	},
	strField("STATS_DB_PATH", true, parseStringValue, func(c *ServerConfig) **string { return &c.StatsDBPath }),
	intField("STATS_DETAIL_RETENTION_DAYS", false, parseNonNegativeIntValue, func(c *ServerConfig) **int { return &c.StatsDetailRetentionDays }),
	intField("STATS_DB_MAX_MB", false, parsePositiveIntValue, func(c *ServerConfig) **int { return &c.StatsDBMaxMB }),
	{
		env: "AGENT_IPC", startupOnly: true,
		parse: func(v any) (any, bool) { return parseAgentIPCValue(v) },
		get: func(c *ServerConfig) any {
			if c.AgentIPC == nil {
				return nil
			}
			return *c.AgentIPC
		},
		set:   func(c *ServerConfig, v any) { c.AgentIPC = v.(*AgentIPCConfig) },
		clear: func(c *ServerConfig) { c.AgentIPC = nil },
		envInput: func() (any, bool) {
			values := map[string]any{
				"ENDPOINT": os.Getenv("AGENT_IPC_ENDPOINT"), "TOKEN": os.Getenv("AGENT_IPC_TOKEN"),
				"DISCOVERY_FILE": os.Getenv("AGENT_IPC_DISCOVERY_FILE"),
				"INSTANCE":       os.Getenv("AGENT_IPC_INSTANCE"),
				"OUTBOX_DIR":     os.Getenv("AGENT_OUTBOX_DIR"),
				"OUTBOX_MAX_MB":  os.Getenv("AGENT_OUTBOX_MAX_MB"),
				"WEBHOOK_OWNER":  os.Getenv("AGENT_WEBHOOK_OWNER"),
			}
			present := false
			for _, value := range values {
				if value != "" {
					present = true
					break
				}
			}
			if !present {
				return nil, false
			}
			return values, true
		},
	},
}

func KnownEnvNames() []string {
	out := make([]string, len(configFields))
	for i, f := range configFields {
		out[i] = f.env
	}
	return out
}

// StartupOnlyEnvNames 返回需要重启才能生效的配置项名。

func StartupOnlyEnvNames() []string {
	var out []string
	for _, f := range configFields {
		if f.startupOnly {
			out = append(out, f.env)
		}
	}
	return out
}

// LoadEnv 从环境变量解析配置。

func LoadEnv() *ServerConfig {
	c := &ServerConfig{}
	for _, f := range configFields {
		var raw any
		var present bool
		if f.envInput != nil {
			raw, present = f.envInput()
		} else {
			s := os.Getenv(f.env)
			raw, present = s, s != ""
		}
		if !present {
			continue
		}
		if v, ok := f.parse(raw); ok {
			f.set(c, v)
		}
	}
	return c
}

// BuildFromMap 从已解析的 map（YAML/JSON 来源，键为大写 ENV 名）构建配置。
// 仅设置 map 中存在且解析有效的字段；monitors 缺省时落地默认 [2]（与 TS 一致）。
