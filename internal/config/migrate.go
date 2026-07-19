package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// MigrationPlan 包含旧配置迁移后生成的完整多文件结果。
// Files 中的路径相对于目标配置目录。
type MigrationPlan struct {
	Files map[string][]byte
}

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

func valueOr(values map[string]any, key string, fallback any) any {
	if value, present := values[key]; present {
		return value
	}
	return fallback
}

func validateLegacyConfig(raw map[string]any) error {
	for _, field := range configFields {
		value, present := raw[field.env]
		if !present {
			continue
		}
		if _, ok := field.parse(value); !ok {
			return fmt.Errorf("invalid value for %s", field.env)
		}
	}
	if value, present := raw["NETUTIL"]; present {
		if err := validateRecordKeys("NETUTIL", value, []string{"DNS_SERVERS"}); err != nil {
			return err
		}
	}
	if value, present := raw["SHARE_STATION"]; present {
		if err := validateRecordKeys("SHARE_STATION", value, []string{"URL", "TOKEN"}); err != nil {
			return err
		}
	}
	if value, present := raw["REDIS"]; present {
		record, _ := asRecord(value)
		if err := validateRecordKeys("REDIS", value, configFileKeys["redis.yaml"]); err != nil {
			return err
		}
		if err := validateRedisMap(record); err != nil {
			return fmt.Errorf("REDIS: %w", err)
		}
	}
	if value, present := raw["WEBHOOK"]; present {
		record, _ := asRecord(value)
		if err := validateRecordKeys("WEBHOOK", value, configFileKeys["webhook.yaml"]); err != nil {
			return err
		}
		if err := validateWebhookMap(record); err != nil {
			return fmt.Errorf("WEBHOOK: %w", err)
		}
	}
	return nil
}

func legacyReplayEnabled(raw map[string]any) bool {
	value, present := raw["REPLAY_ENABLED"]
	if !present {
		return false
	}
	enabled, ok := parseBoolValue(value)
	return ok && enabled
}

func nestedEnabled[T any](raw any, parse func(any) (*T, bool)) bool {
	parsed, ok := parse(raw)
	if !ok || parsed == nil {
		return false
	}
	record, _ := asRecord(raw)
	enabled, _ := parseBoolValue(record["ENABLED"])
	return enabled
}

func pickMap(raw map[string]any, keys []string) map[string]any {
	out := make(map[string]any)
	for _, key := range keys {
		if value, present := raw[key]; present {
			out[key] = value
		}
	}
	return out
}

func hasAny(raw map[string]any, keys []string) bool {
	for _, key := range keys {
		if _, present := raw[key]; present {
			return true
		}
	}
	return false
}

func copyRecord(raw any) map[string]any {
	record, _ := asRecord(raw)
	out := make(map[string]any, len(record))
	for key, value := range record {
		out[key] = value
	}
	return out
}

func (p *MigrationPlan) Names() []string {
	names := make([]string, 0, len(p.Files))
	for name := range p.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Write 创建计划中的所有文件，但不会覆盖现有配置。
// 首次写入前会预先检查全部冲突。
func (p *MigrationPlan) Write(dir string) error {
	for _, name := range p.Names() {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("refusing to overwrite existing file: %s", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range p.Names() {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, p.Files[name], 0o644); err != nil {
			return err
		}
	}
	return nil
}
