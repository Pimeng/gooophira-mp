package load

import "fmt"

// LoadWebhookFile 为 Agent 加载独立的版本化 Webhook 模块。
// 它接受旧版独立 Webhook 结构，但不加载服务端配置或由环境变量管理的核心字段。
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
