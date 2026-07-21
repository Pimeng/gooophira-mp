package load

import (
	"strings"
)

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
