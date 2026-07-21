package load

import (
	"github.com/Pimeng/gooophira-mp/internal/config/migration"
	"github.com/Pimeng/gooophira-mp/internal/config/model"
)

type ServerConfig = model.ServerConfig
type ShareStation = model.ShareStation
type RedisConfig = model.RedisConfig
type OutboundProxy = model.OutboundProxy
type NetutilConfig = model.NetutilConfig
type WebhookTarget = model.WebhookTarget
type WebhookConfig = model.WebhookConfig
type AgentIPCConfig = model.AgentIPCConfig
type AgentStatsConfig = model.AgentStatsConfig
type AgentReplayUploadConfig = model.AgentReplayUploadConfig
type AgentConfig = model.AgentConfig
type Chart = model.Chart
type RecordData = model.RecordData
type MigrationPlan = migration.MigrationPlan

const (
	DefaultServerName               = model.DefaultServerName
	DefaultRoomMaxUsers             = model.DefaultRoomMaxUsers
	DefaultPlayingReconnectGrace    = model.DefaultPlayingReconnectGrace
	DefaultHitokotoAPIURL           = model.DefaultHitokotoAPIURL
	MaxPlayingReconnectGrace        = model.MaxPlayingReconnectGrace
	DefaultReplayTTLDays            = model.DefaultReplayTTLDays
	MaxReplayTTLDays                = model.MaxReplayTTLDays
	DefaultConnectionRateLimit      = model.DefaultConnectionRateLimit
	DefaultHTTPRateLimitMaxRequests = model.DefaultHTTPRateLimitMaxRequests
	DefaultHTTPRateLimitWindowMS    = model.DefaultHTTPRateLimitWindowMS
	DefaultLogLevel                 = model.DefaultLogLevel
	DefaultLogCompressAfterDays     = model.DefaultLogCompressAfterDays
	DefaultLogMaxTotalMB            = model.DefaultLogMaxTotalMB
	MaxRoomMaxUsers                 = model.MaxRoomMaxUsers
	DefaultWebhookTimeoutMS         = model.DefaultWebhookTimeoutMS
	DefaultWebhookRetries           = model.DefaultWebhookRetries
	DefaultStatsDetailRetentionDays = model.DefaultStatsDetailRetentionDays
	DefaultStatsDBMaxMB             = model.DefaultStatsDBMaxMB
	DefaultAgentIPCEndpoint         = model.DefaultAgentIPCEndpoint
	DefaultAgentIPCDiscoveryFile    = model.DefaultAgentIPCDiscoveryFile
	DefaultAgentIPCInstance         = model.DefaultAgentIPCInstance
	DefaultAgentOutboxDir           = model.DefaultAgentOutboxDir
	DefaultAgentOutboxMaxMB         = model.DefaultAgentOutboxMaxMB
	DefaultAgentWebhookOwner        = model.DefaultAgentWebhookOwner
)

var (
	DefaultMonitors       = model.DefaultMonitors
	DefaultTestAccountIDs = model.DefaultTestAccountIDs
	DefaultDNSServers     = model.DefaultDNSServers
)
