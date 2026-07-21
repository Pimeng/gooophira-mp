package model

type AgentIPCConfig struct {
	Endpoint      string
	Token         string
	DiscoveryFile string
	Instance      string
	OutboxDir     string
	OutboxMaxMB   int
	WebhookOwner  string
}

// ShareStation 是回放分享站配置（自动上传到第三方平台用）。

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
