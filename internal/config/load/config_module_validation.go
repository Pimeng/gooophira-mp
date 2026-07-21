package load

import (
	"fmt"
)

func validateRecordKeys(label string, value any, allowedKeys []string) error {
	record, ok := asRecord(value)
	if !ok {
		return fmt.Errorf("%s must be a mapping", label)
	}
	allowed := make(map[string]bool, len(allowedKeys))
	for _, key := range allowedKeys {
		allowed[key] = true
	}
	for key := range record {
		if !allowed[key] {
			return fmt.Errorf("%s contains unknown key %s", label, key)
		}
	}
	return nil
}

func validateRedisMap(raw map[string]any) error {
	if value, present := raw["ENABLED"]; present {
		if _, ok := parseBoolValue(value); !ok {
			return fmt.Errorf("invalid value for ENABLED")
		}
	}
	if value, present := raw["HOST"]; present {
		if _, ok := parseStringValue(value); !ok {
			return fmt.Errorf("invalid value for HOST")
		}
	}
	if value, present := raw["PORT"]; present {
		if _, ok := parsePortValue(value); !ok {
			return fmt.Errorf("invalid value for PORT")
		}
	}
	if value, present := raw["PASSWORD"]; present {
		if _, ok := value.(string); !ok {
			return fmt.Errorf("invalid value for PASSWORD")
		}
	}
	if value, present := raw["DB"]; present {
		if db, ok := asInt(value); !ok || db < 0 {
			return fmt.Errorf("invalid value for DB")
		}
	}
	return nil
}

var webhookTargetKeys = []string{
	"ID", "URL", "TYPE", "EVENTS", "SECRET", "ACCESS_TOKEN", "MESSAGE_TYPE", "TARGET_ID",
	"APP_ID", "APP_SECRET", "RECEIVE_OPEN_ID", "TEMPLATE_ID", "TEMPLATE_VERSION",
	"GAME_END_TEMPLATE_ID", "GAME_END_TEMPLATE_VERSION", "LIVE_UPDATE",
}

func validateWebhookMap(raw map[string]any) error {
	if value, present := raw["ENABLED"]; present {
		if _, ok := parseBoolValue(value); !ok {
			return fmt.Errorf("invalid value for ENABLED")
		}
	}
	if value, present := raw["TIMEOUT_MS"]; present {
		if n, ok := asInt(value); !ok || n <= 0 {
			return fmt.Errorf("invalid value for TIMEOUT_MS")
		}
	}
	if value, present := raw["RETRIES"]; present {
		if n, ok := asInt(value); !ok || n < 0 {
			return fmt.Errorf("invalid value for RETRIES")
		}
	}
	value, present := raw["TARGETS"]
	if !present {
		return nil
	}
	targets, ok := value.([]any)
	if !ok {
		return fmt.Errorf("TARGETS must be a list")
	}
	for i, target := range targets {
		if err := validateRecordKeys(fmt.Sprintf("TARGETS[%d]", i), target, webhookTargetKeys); err != nil {
			return err
		}
	}
	parsed, ok := parseWebhookValue(raw)
	if !ok || len(parsed.Targets) != len(targets) {
		return fmt.Errorf("TARGETS contains an invalid target")
	}
	return nil
}

func buildFromMapStrict(raw map[string]any, path string) (*ServerConfig, error) {
	known := make(map[string]fieldSpec, len(configFields))
	for _, field := range configFields {
		known[field.env] = field
	}
	for key, value := range raw {
		field, ok := known[key]
		if !ok {
			return nil, fmt.Errorf("%s: unsupported configuration key %s", path, key)
		}
		if _, valid := field.parse(value); !valid {
			return nil, fmt.Errorf("%s: invalid value for %s", path, key)
		}
	}
	return buildFromMap(raw, false), nil
}
