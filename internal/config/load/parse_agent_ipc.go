package load

import (
	"strings"
)

func parseAgentIPCValue(v any) (*AgentIPCConfig, bool) {
	m, ok := asRecord(v)
	if !ok {
		return nil, false
	}
	allowed := map[string]bool{"ENDPOINT": true, "TOKEN": true, "DISCOVERY_FILE": true, "INSTANCE": true, "OUTBOX_DIR": true, "OUTBOX_MAX_MB": true, "WEBHOOK_OWNER": true}
	for key := range m {
		if !allowed[key] {
			return nil, false
		}
	}
	endpoint := ""
	if raw, present := m["ENDPOINT"]; present {
		if raw != "" {
			endpoint, ok = parseStringValue(raw)
			if !ok {
				return nil, false
			}
		}
	}
	if endpoint != "" && endpoint != "disabled" && endpoint != "auto" &&
		!strings.HasPrefix(endpoint, "unix://") &&
		!strings.HasPrefix(endpoint, "npipe://") &&
		!strings.HasPrefix(endpoint, "tcp://") {
		return nil, false
	}
	out := &AgentIPCConfig{Endpoint: endpoint}
	for key, target := range map[string]*string{
		"TOKEN": &out.Token, "DISCOVERY_FILE": &out.DiscoveryFile, "INSTANCE": &out.Instance, "OUTBOX_DIR": &out.OutboxDir,
	} {
		if raw, present := m[key]; present {
			if raw == "" {
				continue
			}
			value, valid := parseStringValue(raw)
			if !valid {
				return nil, false
			}
			*target = value
		}
	}
	if raw, present := m["WEBHOOK_OWNER"]; present && raw != "" {
		owner, valid := parseStringValue(raw)
		owner = strings.ToLower(owner)
		if !valid || (owner != "server" && owner != "agent") {
			return nil, false
		}
		out.WebhookOwner = owner
	}
	if raw, present := m["OUTBOX_MAX_MB"]; present && raw != "" {
		value, valid := parsePositiveIntValue(raw)
		if !valid {
			return nil, false
		}
		out.OutboxMaxMB = value
	}
	return out, true
}

// KnownEnvNames 返回所有已知配置项的 ENV/YAML 名。
