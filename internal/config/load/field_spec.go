package load

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
