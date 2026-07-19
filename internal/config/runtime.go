package config

import (
	"sort"
	"strings"
)

// 运行时可热更新配置子系统。对应 TS core/runtimeConfig.ts。
//
// 只有一组精选的「顶层标量」配置项允许在运行时（CLI / HTTP admin）改动并热生效；
// 其余键要么仅启动期生效，要么不支持运行时改动。本模块提供：
//   - 当前生效值快照（GET /admin/runtime-config 展示、回滚取值）；
//   - 补丁解析与分类（合法 / 非法值 / 仅启动期 / 不支持）；
//   - 把补丁应用到 *ServerConfig（含 normalize，如 0 → 视为未设置）。

// runtimeDescriptor 描述一个可热更新配置项。
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
func BuildRuntimeConfigSnapshot(cfg *ServerConfig) map[string]any {
	snap := make(map[string]any, len(runtimeDescriptors))
	for i := range runtimeDescriptors {
		snap[runtimeDescriptors[i].env] = runtimeDescriptors[i].current(cfg)
	}
	return snap
}

// PickRuntimeConfigSnapshot 仅取指定 keys 的当前生效值（用于回滚快照）。
func PickRuntimeConfigSnapshot(cfg *ServerConfig, keys []string) map[string]any {
	snap := BuildRuntimeConfigSnapshot(cfg)
	out := make(map[string]any, len(keys))
	for _, k := range keys {
		env := strings.ToUpper(strings.TrimSpace(k))
		if v, ok := snap[env]; ok {
			out[env] = v
		}
	}
	return out
}

// RuntimePatchResult 是补丁解析结果。OK 时携带应用条目与持久化补丁；否则携带分类后的错误键。
type RuntimePatchResult struct {
	OK   bool
	Keys []string       // 成功解析的 ENV 名（排序）
	rt   []rtPatchEntry // 内部：应用条目
	// Persist 是落盘补丁（ENV 名 → 标量），用于 PersistConfigValues。
	Persist map[string]any

	InvalidKeys     []string // 值非法
	StartupOnlyKeys []string // 仅启动期生效，运行时不可改
	UnsupportedKeys []string // 未知或不支持运行时改动
	Empty           bool     // 无任何可应用键
}

type rtPatchEntry struct {
	desc  *runtimeDescriptor
	value any
}

// Apply 把已解析的补丁应用到 cfg（含 normalize）。仅在 OK 时有意义。
func (r *RuntimePatchResult) Apply(cfg *ServerConfig) {
	for _, e := range r.rt {
		e.desc.apply(cfg, e.value)
	}
}

// ParseRuntimeConfigPatch 解析并校验一份补丁（键为 ENV 名，大小写/空白不敏感）。
// 对应 TS parseRuntimeConfigPatch：未知/仅启动期/值非法分别归类，全部无效则 Empty。
func ParseRuntimeConfigPatch(raw map[string]any) RuntimePatchResult {
	persist := make(map[string]any)
	var entries []rtPatchEntry
	var invalid, startupOnly, unsupported []string

	for rawKey, rawValue := range raw {
		env := strings.ToUpper(strings.TrimSpace(rawKey))
		desc := runtimeDescByEnv(env)
		if desc == nil {
			if startupOnlyEnvSet[env] {
				startupOnly = append(startupOnly, env)
			} else {
				unsupported = append(unsupported, env) // 已知非运行时项或未知项
			}
			continue
		}
		v, ok := desc.parse(rawValue)
		if !ok {
			invalid = append(invalid, env)
			continue
		}
		persist[env] = v
		entries = append(entries, rtPatchEntry{desc: desc, value: v})
	}

	if len(persist) == 0 {
		sort.Strings(invalid)
		sort.Strings(startupOnly)
		sort.Strings(unsupported)
		return RuntimePatchResult{OK: false, InvalidKeys: invalid, StartupOnlyKeys: startupOnly, UnsupportedKeys: unsupported, Empty: true}
	}

	keys := make([]string, 0, len(persist))
	for k := range persist {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return RuntimePatchResult{OK: true, Keys: keys, rt: entries, Persist: persist}
}
