package config

const (
	DefaultServerName               = "Phira MP"
	DefaultRoomMaxUsers             = 512
	DefaultPlayingReconnectGrace    = 5
	DefaultHitokotoAPIURL           = "https://v1.hitokoto.cn/"
	MaxPlayingReconnectGrace        = 120
	DefaultReplayTTLDays            = 4
	MaxReplayTTLDays                = 3650
	DefaultConnectionRateLimit      = 30
	DefaultHTTPRateLimitMaxRequests = 100
	DefaultHTTPRateLimitWindowMS    = 60000
	DefaultLogLevel                 = "INFO"
	DefaultLogCompressAfterDays     = 14
	DefaultLogMaxTotalMB            = 500
	MaxRoomMaxUsers                 = 32767
	DefaultWebhookTimeoutMS         = 5000
	DefaultWebhookRetries           = 2
	DefaultStatsDetailRetentionDays = 90
	DefaultStatsDBMaxMB             = 500
	DefaultAgentIPCEndpoint         = "disabled"
	DefaultAgentIPCDiscoveryFile    = "agent-ipc.json"
	DefaultAgentIPCInstance         = "default"
	DefaultAgentOutboxDir           = "agent-outbox"
	DefaultAgentOutboxMaxMB         = 64
	DefaultAgentWebhookOwner        = "agent"
)

var (
	DefaultMonitors       = []int{2}
	DefaultTestAccountIDs = []int{1739989}
	DefaultDNSServers     = []string{"1.1.1.1:53", "8.8.8.8:53"}
)
