package load

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

func BuildMigrationPlan(legacyPath string) (*MigrationPlan, error) {
	data, err := os.ReadFile(legacyPath)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", legacyPath, err)
	}
	if raw == nil {
		return nil, fmt.Errorf("%s: configuration must be a YAML mapping", legacyPath)
	}

	known := make(map[string]bool, len(configFields))
	for _, field := range configFields {
		known[field.env] = true
	}
	for key := range raw {
		if !known[key] {
			return nil, fmt.Errorf("%s: unknown configuration key %s", legacyPath, key)
		}
	}
	if err := validateLegacyConfig(raw); err != nil {
		return nil, fmt.Errorf("%s: %w", legacyPath, err)
	}

	files := make(map[string]map[string]any)
	files[CoreConfigFile] = pickMap(raw, configFileKeys[CoreConfigFile])
	files[CoreConfigFile]["AGENT_IPC"] = migratedAgentIPC(raw["AGENT_IPC"])

	if hasAny(raw, configFileKeys["network.yaml"]) {
		files["network.yaml"] = pickMap(raw, configFileKeys["network.yaml"])
	}
	if legacyReplayEnabled(raw) {
		files["replay.yaml"] = pickMap(raw, configFileKeys["replay.yaml"])
		delete(files["replay.yaml"], "REPLAY_ENABLED")
		delete(files["replay.yaml"], "REPLAY_AUTO_UPLOAD")
		delete(files["replay.yaml"], "SHARE_STATION")
	}
	if nestedEnabled(raw["REDIS"], parseRedisValue) {
		files["redis.yaml"] = copyRecord(raw["REDIS"])
		delete(files["redis.yaml"], "ENABLED")
	}

	// 统计、Webhook 投递和回放上传现由 Agent 管理。
	// 旧模式总会初始化统计，因此每次迁移都会启用 Agent 边界并生成 agent.yaml，
	// 以保留原有行为。
	files["agent.yaml"] = migratedAgentConfig(raw)

	plan := &MigrationPlan{Files: make(map[string][]byte, len(files))}
	for name, values := range files {
		values["version"] = CurrentFileVersion
		encoded, err := yaml.Marshal(values)
		if err != nil {
			return nil, err
		}
		plan.Files[name] = encoded
	}
	return plan, nil
}

func migratedAgentIPC(raw any) map[string]any {
	out := copyRecord(raw)
	out["ENDPOINT"] = "auto"
	out["WEBHOOK_OWNER"] = "agent"
	return out
}

func migratedAgentConfig(raw map[string]any) map[string]any {
	webhook := copyRecord(raw["WEBHOOK"])
	enabled, _ := parseBoolValue(webhook["ENABLED"])
	out := map[string]any{
		"ENABLED":    enabled,
		"TIMEOUT_MS": valueOr(webhook, "TIMEOUT_MS", DefaultWebhookTimeoutMS),
		"RETRIES":    valueOr(webhook, "RETRIES", DefaultWebhookRetries),
		"TARGETS":    valueOr(webhook, "TARGETS", []any{}),
		"STATS": map[string]any{
			"ENABLED":               true,
			"DB_PATH":               valueOr(raw, "STATS_DB_PATH", "stats.db"),
			"DETAIL_RETENTION_DAYS": valueOr(raw, "STATS_DETAIL_RETENTION_DAYS", DefaultStatsDetailRetentionDays),
			"DB_MAX_MB":             valueOr(raw, "STATS_DB_MAX_MB", DefaultStatsDBMaxMB),
		},
	}

	station := copyRecord(raw["SHARE_STATION"])
	url, urlOK := parseStringValue(station["URL"])
	token, tokenOK := parseStringValue(station["TOKEN"])
	if urlOK || tokenOK {
		autoUpload, _ := parseBoolValue(raw["REPLAY_AUTO_UPLOAD"])
		baseDir, ok := parseStringValue(raw["REPLAY_BASE_DIR"])
		if !ok {
			baseDir = "./record"
		}
		out["REPLAY_UPLOAD"] = map[string]any{
			"ENABLED":     urlOK && tokenOK && url != "" && token != "",
			"AUTO_UPLOAD": autoUpload,
			"BASE_DIR":    baseDir,
			"URL":         url,
			"TOKEN":       token,
			"STATE_PATH":  "agent-inbox/upload-state.json",
			"DELAY_MS":    30000,
		}
	}
	return out
}
