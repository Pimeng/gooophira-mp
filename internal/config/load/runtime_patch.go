package load

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
