package load

import (
	"strings"
)

type runtimeDescriptor struct {
	env     string
	parse   func(any) (any, bool)    // 解析补丁原始值（持久化用此值）
	current func(*ServerConfig) any  // 当前生效值（含默认）
	apply   func(*ServerConfig, any) // 应用到配置（含 normalize）
}

// fieldByEnv 按 ENV 名查 configFields 元数据（复用其 set/clear，避免重复定义指针闭包）。

func fieldByEnv(env string) *fieldSpec {
	for i := range configFields {
		if configFields[i].env == env {
			return &configFields[i]
		}
	}
	panic("config: unknown runtime field env " + env) // 仅初始化期触发，编码错误
}

// 解析适配器：把 (T,bool) 解析函数适配成 (any,bool)。

func pBool(v any) (any, bool) { b, ok := parseBoolValue(v); return b, ok }

func pInt(f func(any) (int, bool)) func(any) (any, bool) {
	return func(v any) (any, bool) { n, ok := f(v); return n, ok }
}

func pStr(f func(any) (string, bool)) func(any) (any, bool) {
	return func(v any) (any, bool) { s, ok := f(v); return s, ok }
}

// rtField 构造一个直接 set 的运行时描述项（无 normalize）。

func rtField(env string, parse func(any) (any, bool), current func(*ServerConfig) any) runtimeDescriptor {
	f := fieldByEnv(env)
	return runtimeDescriptor{env: env, parse: parse, current: current, apply: func(c *ServerConfig, v any) { f.set(c, v) }}
}

// rtNormInt 构造「0 视为未设置（清除）」的整型运行时描述项（MAX_ROOMS / MAX_CONNECTIONS）。

func rtNormInt(env string, current func(*ServerConfig) any) runtimeDescriptor {
	f := fieldByEnv(env)
	return runtimeDescriptor{
		env: env, parse: pInt(parseNonNegativeIntValue), current: current,
		apply: func(c *ServerConfig, v any) {
			if n := v.(int); n > 0 {
				f.set(c, n)
			} else {
				f.clear(c)
			}
		},
	}
}

// parseRuntimeRoomListTip 解析房间列表提示：null→""（清除），非字符串非法，字符串取 trim。

func parseRuntimeRoomListTip(v any) (any, bool) {
	if v == nil {
		return "", true
	}
	s, ok := v.(string)
	if !ok {
		return nil, false
	}
	return strings.TrimSpace(s), true
}

// rtTip 构造 ROOM_LIST_TIP 描述项（trim 后为空则清除）。

func rtTip() runtimeDescriptor {
	f := fieldByEnv("ROOM_LIST_TIP")
	return runtimeDescriptor{
		env: "ROOM_LIST_TIP", parse: parseRuntimeRoomListTip,
		current: func(c *ServerConfig) any { return c.EffectiveRoomListTip() },
		apply: func(c *ServerConfig, v any) {
			if s := strings.TrimSpace(v.(string)); s != "" {
				f.set(c, s)
			} else {
				f.clear(c)
			}
		},
	}
}

// runtimeDescriptors 是全部可热更新配置项（顺序对齐 TS RUNTIME_CONFIG_DESCRIPTORS）。

var runtimeDescriptors = []runtimeDescriptor{
	rtField("ROOM_CREATION_ENABLED", pBool, func(c *ServerConfig) any { return c.EffectiveRoomCreationEnabled() }),
	rtField("REPLAY_ENABLED", pBool, func(c *ServerConfig) any { return c.EffectiveReplayEnabled() }),
	rtField("ROOM_MAX_USERS", pInt(parseRoomMaxUsersValue), func(c *ServerConfig) any { return c.EffectiveRoomMaxUsers() }),
	rtField("PLAYING_RECONNECT_GRACE", pInt(parsePlayingGraceValue), func(c *ServerConfig) any { return c.EffectivePlayingReconnectGrace() }),
	rtNormInt("MAX_ROOMS", func(c *ServerConfig) any { return c.EffectiveMaxRooms() }),
	rtNormInt("MAX_CONNECTIONS", func(c *ServerConfig) any { return c.EffectiveMaxConnections() }),
	rtField("CONNECTION_RATE_LIMIT", pInt(parsePositiveIntValue), func(c *ServerConfig) any { return c.EffectiveConnectionRateLimit() }),
	rtField("COMMAND_RATE_LIMIT", pBool, func(c *ServerConfig) any { return c.EffectiveCommandRateLimit() }),
	rtField("HTTP_RATE_LIMIT_MAX_REQUESTS", pInt(parsePositiveIntValue), func(c *ServerConfig) any { return c.EffectiveHTTPRateLimitMaxRequests() }),
	rtField("HTTP_RATE_LIMIT_WINDOW_MS", pInt(parsePositiveIntValue), func(c *ServerConfig) any { return c.EffectiveHTTPRateLimitWindowMS() }),
	rtField("CHAT_ENABLED", pBool, func(c *ServerConfig) any { return c.EffectiveChatEnabled() }),
	rtField("REPLAY_TTL_DAYS", pInt(parseReplayTTLDaysValue), func(c *ServerConfig) any { return c.EffectiveReplayTTLDays() }),
	rtTip(),
	rtField("LOG_LEVEL", pStr(parseLogLevelValue), func(c *ServerConfig) any { return c.EffectiveLogLevel() }),
	rtField("LOG_COMPRESS_AFTER_DAYS", pInt(parseNonNegativeIntValue), func(c *ServerConfig) any { return c.EffectiveLogCompressAfterDays() }),
	rtField("LOG_MAX_TOTAL_MB", pInt(parseNonNegativeIntValue), func(c *ServerConfig) any { return c.EffectiveLogMaxTotalMB() }),
}

// startupOnlyEnvSet 是仅启动期生效的 ENV 名集合（用于补丁分类）。

var startupOnlyEnvSet = func() map[string]bool {
	m := make(map[string]bool)
	for _, env := range StartupOnlyEnvNames() {
		m[env] = true
	}
	return m
}()

func runtimeDescByEnv(env string) *runtimeDescriptor {
	for i := range runtimeDescriptors {
		if runtimeDescriptors[i].env == env {
			return &runtimeDescriptors[i]
		}
	}
	return nil
}

// RuntimeConfigEnvNames 返回全部可热更新配置项的 ENV 名（顺序固定）。

func RuntimeConfigEnvNames() []string {
	out := make([]string, len(runtimeDescriptors))
	for i := range runtimeDescriptors {
		out[i] = runtimeDescriptors[i].env
	}
	return out
}

// BuildRuntimeConfigSnapshot 返回全部可热更新项的当前生效值（含默认）。
