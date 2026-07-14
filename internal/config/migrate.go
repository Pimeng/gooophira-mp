package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// MigrationPlan contains the exact multi-file output of a legacy migration.
// Files are relative to the destination configuration directory.
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

	if hasAny(raw, configFileKeys["network.yaml"]) {
		files["network.yaml"] = pickMap(raw, configFileKeys["network.yaml"])
	}
	if legacyReplayEnabled(raw) {
		files["replay.yaml"] = pickMap(raw, configFileKeys["replay.yaml"])
		delete(files["replay.yaml"], "REPLAY_ENABLED")
	}
	if nestedEnabled(raw["REDIS"], parseRedisValue) {
		files["redis.yaml"] = copyRecord(raw["REDIS"])
		delete(files["redis.yaml"], "ENABLED")
	}
	if nestedEnabled(raw["WEBHOOK"], parseWebhookValue) {
		files["webhook.yaml"] = copyRecord(raw["WEBHOOK"])
		delete(files["webhook.yaml"], "ENABLED")
	}

	// Legacy mode always initializes statistics, so create stats.yaml even when
	// no tuning keys were present. This preserves behavior across migration.
	files["stats.yaml"] = pickMap(raw, configFileKeys["stats.yaml"])

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

// Write creates every planned file without overwriting existing configuration.
// All conflicts are checked before the first write.
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
