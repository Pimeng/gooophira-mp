package load

import (
	"fmt"
)

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
