package config

import "fmt"

// LoadWebhookFile loads a standalone versioned webhook module for the Agent.
// It accepts the same schema as config/webhook.yaml without loading server
// configuration or environment-owned core fields.
func LoadWebhookFile(path string) (*WebhookConfig, error) {
	raw, err := loadVersionedMap(path, configFileKeys["webhook.yaml"], true)
	if err != nil {
		return nil, err
	}
	if err := validateWebhookMap(raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if _, present := raw["ENABLED"]; !present {
		raw["ENABLED"] = true
	}
	parsed, ok := parseWebhookValue(raw)
	if !ok {
		return nil, fmt.Errorf("%s: invalid webhook configuration", path)
	}
	return parsed, nil
}
