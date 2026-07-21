package load

func normalizeModuleMap(name string, raw map[string]any) (map[string]any, error) {
	switch name {
	case "network.yaml":
		if value, present := raw["NETUTIL"]; present {
			if err := validateRecordKeys("NETUTIL", value, []string{"DNS_SERVERS"}); err != nil {
				return nil, err
			}
		}
		return raw, nil
	case "replay.yaml":
		if value, present := raw["SHARE_STATION"]; present {
			if err := validateRecordKeys("SHARE_STATION", value, []string{"URL", "TOKEN"}); err != nil {
				return nil, err
			}
		}
		if _, present := raw["REPLAY_ENABLED"]; !present {
			raw["REPLAY_ENABLED"] = true
		}
		return raw, nil
	case "redis.yaml":
		if err := validateRedisMap(raw); err != nil {
			return nil, err
		}
		if _, present := raw["ENABLED"]; !present {
			raw["ENABLED"] = true
		}
		return map[string]any{"REDIS": raw}, nil
	case "webhook.yaml":
		if err := validateWebhookMap(raw); err != nil {
			return nil, err
		}
		if _, present := raw["ENABLED"]; !present {
			raw["ENABLED"] = true
		}
		return map[string]any{"WEBHOOK": raw}, nil
	case "stats.yaml":
		if _, present := raw["STATS_DB_PATH"]; !present {
			raw["STATS_DB_PATH"] = "stats.db"
		}
		return raw, nil
	case CoreConfigFile:
		if value, present := raw["AGENT_IPC"]; present {
			if err := validateRecordKeys("AGENT_IPC", value, []string{"ENDPOINT", "TOKEN", "DISCOVERY_FILE", "INSTANCE", "OUTBOX_DIR", "OUTBOX_MAX_MB", "WEBHOOK_OWNER"}); err != nil {
				return nil, err
			}
		}
		return raw, nil
	default:
		return raw, nil
	}
}
