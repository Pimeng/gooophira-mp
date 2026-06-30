package config

import (
	"os"
	"reflect"
)

// 配置字段元数据表：一处定义，驱动 env 加载、map 构建、合并、差异比对、startup-only 分类。
//
// get 返回解引用后的值（未设置为 nil）；set 从 parse 结果写回指针字段。用「指针字段的
// 地址」闭包保持类型安全，避免反射。

type fieldSpec struct {
	env         string
	startupOnly bool
	parse       func(any) (any, bool)
	get         func(*ServerConfig) any
	set         func(*ServerConfig, any)
	clear       func(*ServerConfig) // 把该字段置回未设置（nil）
	// envInput 自定义环境变量读取（如 LANG 兜底、SHARE_STATION/REDIS 多变量合成）；
	// 返回 (输入, 是否有输入)。nil 表示用 os.Getenv(env)。
	envInput func() (any, bool)
}

func intField(env string, startupOnly bool, parse func(any) (int, bool), ptr func(*ServerConfig) **int) fieldSpec {
	return fieldSpec{
		env: env, startupOnly: startupOnly,
		parse: func(v any) (any, bool) { return parse(v) },
		get: func(c *ServerConfig) any {
			p := *ptr(c)
			if p == nil {
				return nil
			}
			return *p
		},
		set:   func(c *ServerConfig, v any) { n := v.(int); *ptr(c) = &n },
		clear: func(c *ServerConfig) { *ptr(c) = nil },
	}
}

func boolField(env string, startupOnly bool, ptr func(*ServerConfig) **bool) fieldSpec {
	return fieldSpec{
		env: env, startupOnly: startupOnly,
		parse: func(v any) (any, bool) { return parseBoolValue(v) },
		get: func(c *ServerConfig) any {
			p := *ptr(c)
			if p == nil {
				return nil
			}
			return *p
		},
		set:   func(c *ServerConfig, v any) { b := v.(bool); *ptr(c) = &b },
		clear: func(c *ServerConfig) { *ptr(c) = nil },
	}
}

func strField(env string, startupOnly bool, parse func(any) (string, bool), ptr func(*ServerConfig) **string) fieldSpec {
	return fieldSpec{
		env: env, startupOnly: startupOnly,
		parse: func(v any) (any, bool) { return parse(v) },
		get: func(c *ServerConfig) any {
			p := *ptr(c)
			if p == nil {
				return nil
			}
			return *p
		},
		set:   func(c *ServerConfig, v any) { s := v.(string); *ptr(c) = &s },
		clear: func(c *ServerConfig) { *ptr(c) = nil },
	}
}

func intListField(env string, ptr func(*ServerConfig) *[]int) fieldSpec {
	return fieldSpec{
		env:   env,
		parse: func(v any) (any, bool) { return parseIntegerListValue(v) },
		get: func(c *ServerConfig) any {
			p := *ptr(c)
			if p == nil {
				return nil
			}
			return p
		},
		set:   func(c *ServerConfig, v any) { *ptr(c) = v.([]int) },
		clear: func(c *ServerConfig) { *ptr(c) = nil },
	}
}

func strListField(env string, ptr func(*ServerConfig) *[]string) fieldSpec {
	return fieldSpec{
		env:   env,
		parse: func(v any) (any, bool) { return parseStringListValue(v) },
		get: func(c *ServerConfig) any {
			p := *ptr(c)
			if p == nil {
				return nil
			}
			return p
		},
		set:   func(c *ServerConfig, v any) { *ptr(c) = v.([]string) },
		clear: func(c *ServerConfig) { *ptr(c) = nil },
	}
}

// configFields 是全部配置字段的元数据表（顺序与 TS CONFIG_FIELDS 一致）。
var configFields = []fieldSpec{
	intListField("MONITORS", func(c *ServerConfig) *[]int { return &c.Monitors }),
	intListField("TEST_ACCOUNT_IDS", func(c *ServerConfig) *[]int { return &c.TestAccountIDs }),
	strField("SERVER_NAME", false, parseStringValue, func(c *ServerConfig) **string { return &c.ServerName }),
	strField("HOST", true, parseStringValue, func(c *ServerConfig) **string { return &c.Host }),
	intField("PORT", true, parsePortValue, func(c *ServerConfig) **int { return &c.Port }),
	boolField("HTTP_SERVICE", true, func(c *ServerConfig) **bool { return &c.HTTPService }),
	intField("HTTP_PORT", true, parsePortValue, func(c *ServerConfig) **int { return &c.HTTPPort }),
	boolField("GUI", true, func(c *ServerConfig) **bool { return &c.GUI }),
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
		// WEBHOOK：嵌套含目标列表，仅经 YAML 配置（无 env 合成）；非 startup-only，支持热重载。
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
	strField("STATS_DB_PATH", false, parseStringValue, func(c *ServerConfig) **string { return &c.StatsDBPath }),
	intField("STATS_DETAIL_RETENTION_DAYS", false, parseNonNegativeIntValue, func(c *ServerConfig) **int { return &c.StatsDetailRetentionDays }),
	intField("STATS_DB_MAX_MB", false, parsePositiveIntValue, func(c *ServerConfig) **int { return &c.StatsDBMaxMB }),
}

// KnownEnvNames 返回所有已知配置项的 ENV/YAML 名。
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
func BuildFromMap(raw map[string]any) *ServerConfig {
	c := &ServerConfig{}
	for _, f := range configFields {
		v, present := raw[f.env]
		if !present {
			continue
		}
		if parsed, ok := f.parse(v); ok {
			f.set(c, parsed)
		}
	}
	if c.Monitors == nil {
		c.Monitors = append([]int(nil), DefaultMonitors...)
	}
	return c
}

// Merge 合并配置：override 非空字段覆盖 base，其余用 base 兜底；
// monitors / test_account_ids 缺省落地默认值（与 TS mergeConfig 一致）。
func Merge(base, override *ServerConfig) *ServerConfig {
	merged := &ServerConfig{}
	for _, f := range configFields {
		if v := f.get(override); v != nil {
			f.set(merged, ensureParsed(v))
		} else if v := f.get(base); v != nil {
			f.set(merged, ensureParsed(v))
		}
	}
	if merged.Monitors == nil {
		merged.Monitors = append([]int(nil), DefaultMonitors...)
	}
	if merged.TestAccountIDs == nil {
		merged.TestAccountIDs = append([]int(nil), DefaultTestAccountIDs...)
	}
	return merged
}

// ensureParsed 把 get() 返回的解引用值还原成 set() 期望的形态（标量直传，
// 子结构需取地址）。get 对子结构返回值类型，set 期望指针类型，这里桥接。
func ensureParsed(v any) any {
	switch x := v.(type) {
	case OutboundProxy:
		return &x
	case ShareStation:
		return &x
	case RedisConfig:
		return &x
	case WebhookConfig:
		return &x
	default:
		return v
	}
}

// ChangedKeys 返回 prev 与 next 之间值不同的字段 ENV 名列表。
func ChangedKeys(prev, next *ServerConfig) []string {
	var out []string
	for _, f := range configFields {
		if !reflect.DeepEqual(f.get(prev), f.get(next)) {
			out = append(out, f.env)
		}
	}
	return out
}

// KeepStartupOnly 把 next 中所有 startup-only 字段还原为 prev 的值，返回调整后的
// 配置与「需重启才能生效」的字段名列表。
func KeepStartupOnly(prev, next *ServerConfig) (*ServerConfig, []string) {
	out := *next // 浅拷贝
	cfg := &out
	var restart []string
	for _, f := range configFields {
		if !f.startupOnly {
			continue
		}
		if !reflect.DeepEqual(f.get(prev), f.get(next)) {
			if v := f.get(prev); v != nil {
				f.set(cfg, ensureParsed(v))
			} else {
				f.clear(cfg) // prev 未设置 → 把 next 该字段也清回未设置
			}
			restart = append(restart, f.env)
		}
	}
	return cfg, restart
}
