package load

import (
	"reflect"
)

func ChangedKeys(prev, next *ServerConfig) []string {
	var out []string
	for _, f := range configFields {
		if !reflect.DeepEqual(f.get(prev), f.get(next)) {
			out = append(out, f.env)
		}
	}
	return out
}

// KeepStartupOnly 把 next 中所有仅启动期字段还原为 prev 的值，返回调整后的
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
