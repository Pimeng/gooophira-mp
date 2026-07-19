package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type AgentStatsConfig struct {
	Enabled             bool
	DBPath              string
	DetailRetentionDays int
	DBMaxMB             int
}

type AgentReplayUploadConfig struct {
	Enabled    bool
	AutoUpload bool
	BaseDir    string
	URL        string
	Token      string
	StatePath  string
	DelayMS    int
}

type AgentConfig struct {
	Webhook      *WebhookConfig
	Stats        AgentStatsConfig
	ReplayUpload AgentReplayUploadConfig
}

// LoadAgentFile loads configuration owned exclusively by cmd/agent. Webhook
// keys remain top-level for compatibility with the initial Agent slice.
func LoadAgentFile(path string) (*AgentConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	version, ok := asInt(raw["version"])
	if !ok {
		version, ok = asInt(raw["VERSION"])
	}
	if !ok || version != CurrentFileVersion {
		return nil, fmt.Errorf("%s: unsupported or missing version", path)
	}
	delete(raw, "version")
	delete(raw, "VERSION")
	allowed := map[string]bool{"ENABLED": true, "TIMEOUT_MS": true, "RETRIES": true, "TARGETS": true, "STATS": true, "REPLAY_UPLOAD": true}
	for key := range raw {
		if !allowed[key] {
			return nil, fmt.Errorf("%s: unknown Agent configuration key %s", path, key)
		}
	}
	webhookRaw := make(map[string]any)
	for _, key := range []string{"ENABLED", "TIMEOUT_MS", "RETRIES", "TARGETS"} {
		if value, present := raw[key]; present {
			webhookRaw[key] = value
		}
	}
	if err := validateWebhookMap(webhookRaw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if _, present := webhookRaw["ENABLED"]; !present {
		webhookRaw["ENABLED"] = true
	}
	webhookCfg, ok := parseWebhookValue(webhookRaw)
	if !ok {
		return nil, fmt.Errorf("%s: invalid webhook configuration", path)
	}
	statsCfg := AgentStatsConfig{DBPath: "stats.db", DetailRetentionDays: DefaultStatsDetailRetentionDays, DBMaxMB: DefaultStatsDBMaxMB}
	if value, present := raw["STATS"]; present {
		record, ok := asRecord(value)
		if !ok {
			return nil, fmt.Errorf("%s: STATS must be a mapping", path)
		}
		for key := range record {
			if key != "ENABLED" && key != "DB_PATH" && key != "DETAIL_RETENTION_DAYS" && key != "DB_MAX_MB" {
				return nil, fmt.Errorf("%s: unknown STATS key %s", path, key)
			}
		}
		statsCfg.Enabled, _ = parseBoolValue(record["ENABLED"])
		if dbPath, valid := parseStringValue(record["DB_PATH"]); valid {
			statsCfg.DBPath = dbPath
		}
		if days, valid := parseNonNegativeIntValue(record["DETAIL_RETENTION_DAYS"]); valid {
			statsCfg.DetailRetentionDays = days
		}
		if maxMB, valid := parsePositiveIntValue(record["DB_MAX_MB"]); valid {
			statsCfg.DBMaxMB = maxMB
		}
	}
	statsCfg.DBPath = strings.TrimSpace(statsCfg.DBPath)
	replayCfg := AgentReplayUploadConfig{BaseDir: "replays", StatePath: "agent-inbox/upload-state.json", DelayMS: 30000}
	if value, present := raw["REPLAY_UPLOAD"]; present {
		record, ok := asRecord(value)
		if !ok {
			return nil, fmt.Errorf("%s: REPLAY_UPLOAD must be a mapping", path)
		}
		allowedReplay := map[string]bool{"ENABLED": true, "AUTO_UPLOAD": true, "BASE_DIR": true, "URL": true, "TOKEN": true, "STATE_PATH": true, "DELAY_MS": true}
		for key := range record {
			if !allowedReplay[key] {
				return nil, fmt.Errorf("%s: unknown REPLAY_UPLOAD key %s", path, key)
			}
		}
		replayCfg.Enabled, _ = parseBoolValue(record["ENABLED"])
		replayCfg.AutoUpload, _ = parseBoolValue(record["AUTO_UPLOAD"])
		if v, ok := parseStringValue(record["BASE_DIR"]); ok {
			replayCfg.BaseDir = strings.TrimSpace(v)
		}
		if v, ok := parseStringValue(record["URL"]); ok {
			replayCfg.URL = strings.TrimRight(strings.TrimSpace(v), "/")
		}
		if v, ok := parseStringValue(record["TOKEN"]); ok {
			replayCfg.Token = strings.TrimSpace(v)
		}
		if v, ok := parseStringValue(record["STATE_PATH"]); ok {
			replayCfg.StatePath = strings.TrimSpace(v)
		}
		if v, ok := parsePositiveIntValue(record["DELAY_MS"]); ok {
			replayCfg.DelayMS = v
		}
	}
	if replayCfg.Enabled && (replayCfg.BaseDir == "" || replayCfg.URL == "" || replayCfg.Token == "" || replayCfg.StatePath == "") {
		return nil, fmt.Errorf("%s: enabled REPLAY_UPLOAD requires BASE_DIR, URL, TOKEN, and STATE_PATH", path)
	}
	return &AgentConfig{Webhook: webhookCfg, Stats: statsCfg, ReplayUpload: replayCfg}, nil
}
