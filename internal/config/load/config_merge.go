package load

func BuildFromMap(raw map[string]any) *ServerConfig {
	return buildFromMap(raw, true)
}

func buildFromMap(raw map[string]any, applyDefaults bool) *ServerConfig {
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
	if applyDefaults && c.Monitors == nil {
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
	merged.AgentIPC = mergeAgentIPC(base.AgentIPC, override.AgentIPC)
	return merged
}

func mergeAgentIPC(base, override *AgentIPCConfig) *AgentIPCConfig {
	if base == nil && override == nil {
		return nil
	}
	var out AgentIPCConfig
	if base != nil {
		out = *base
	}
	if override != nil {
		if override.Endpoint != "" {
			out.Endpoint = override.Endpoint
		}
		if override.Token != "" {
			out.Token = override.Token
		}
		if override.DiscoveryFile != "" {
			out.DiscoveryFile = override.DiscoveryFile
		}
		if override.Instance != "" {
			out.Instance = override.Instance
		}
		if override.OutboxDir != "" {
			out.OutboxDir = override.OutboxDir
		}
		if override.OutboxMaxMB > 0 {
			out.OutboxMaxMB = override.OutboxMaxMB
		}
		if override.WebhookOwner != "" {
			out.WebhookOwner = override.WebhookOwner
		}
	}
	return &out
}

// ensureParsed 把 get() 返回的解引用值还原成 set() 期望的形态（标量直传，
// 子结构需取地址）。get 对子结构返回值类型，set 期望指针类型，这里桥接。

func ensureParsed(v any) any {
	switch x := v.(type) {
	case OutboundProxy:
		return &x
	case NetutilConfig:
		return &x
	case ShareStation:
		return &x
	case RedisConfig:
		return &x
	case WebhookConfig:
		return &x
	case AgentIPCConfig:
		return &x
	default:
		return v
	}
}

// ChangedKeys 返回 prev 与 next 之间值不同的字段 ENV 名列表。
