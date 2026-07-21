package config

import (
	"time"

	impl "github.com/Pimeng/gooophira-mp/internal/config/load"
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
type ConfigSet = impl.ConfigSet
type FileWatcher = impl.FileWatcher
type RuntimePatchResult = impl.RuntimePatchResult

const (
	CurrentFileVersion              = impl.CurrentFileVersion
	CoreConfigFile                  = impl.CoreConfigFile
	MinimalServerYAML               = impl.MinimalServerYAML
	DefaultConfigYAML               = impl.DefaultConfigYAML
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

func LoadFile(path string) (*ServerConfig, error)                  { return impl.LoadFile(path) }
func LoadMerged(path string) (*ServerConfig, bool, error)          { return impl.LoadMerged(path) }
func LoadDir(dir string) (*ConfigSet, error)                       { return impl.LoadDir(dir) }
func ConfigFileNames() []string                                    { return impl.ConfigFileNames() }
func ConfigDirExists(dir string) bool                              { return impl.ConfigDirExists(dir) }
func EnsureConfigDir(dir string) (bool, error)                     { return impl.EnsureConfigDir(dir) }
func EnsureDefaultFile(path string) (bool, error)                  { return impl.EnsureDefaultFile(path) }
func LoadAgentFile(path string) (*AgentConfig, error)              { return impl.LoadAgentFile(path) }
func LoadWebhookFile(path string) (*WebhookConfig, error)          { return impl.LoadWebhookFile(path) }
func BuildMigrationPlan(path string) (*MigrationPlan, error)       { return impl.BuildMigrationPlan(path) }
func BuildFromMap(raw map[string]any) *ServerConfig                { return impl.BuildFromMap(raw) }
func Merge(base, override *ServerConfig) *ServerConfig             { return impl.Merge(base, override) }
func LoadEnv() *ServerConfig                                       { return impl.LoadEnv() }
func KnownEnvNames() []string                                      { return impl.KnownEnvNames() }
func StartupOnlyEnvNames() []string                                { return impl.StartupOnlyEnvNames() }
func ChangedKeys(a, b *ServerConfig) []string                      { return impl.ChangedKeys(a, b) }
func KeepStartupOnly(a, b *ServerConfig) (*ServerConfig, []string) { return impl.KeepStartupOnly(a, b) }
func BuildRuntimeConfigSnapshot(c *ServerConfig) map[string]any {
	return impl.BuildRuntimeConfigSnapshot(c)
}
func PickRuntimeConfigSnapshot(c *ServerConfig, keys []string) map[string]any {
	return impl.PickRuntimeConfigSnapshot(c, keys)
}
func ParseRuntimeConfigPatch(raw map[string]any) RuntimePatchResult {
	return impl.ParseRuntimeConfigPatch(raw)
}
func RuntimeConfigEnvNames() []string { return impl.RuntimeConfigEnvNames() }
func ApplyConfigUpdates(text string, updates map[string]any) string {
	return impl.ApplyConfigUpdates(text, updates)
}
func PersistConfigValues(path string, updates map[string]any) error {
	return impl.PersistConfigValues(path, updates)
}
func PersistConfigDirValues(dir string, updates map[string]any) error {
	return impl.PersistConfigDirValues(dir, updates)
}
func ParsePort(v any) (int, bool)          { return impl.ParsePort(v) }
func ParseRoomMaxUsers(v any) (int, bool)  { return impl.ParseRoomMaxUsers(v) }
func ParseIntegerList(v any) ([]int, bool) { return impl.ParseIntegerList(v) }
func ParseBool(v any) (bool, bool)         { return impl.ParseBool(v) }
func ParseString(v any) (string, bool)     { return impl.ParseString(v) }
func ParseInteger(v any) (int, bool)       { return impl.ParseInteger(v) }
func NewFileWatcher(path string, interval time.Duration, cb func()) *FileWatcher {
	return impl.NewFileWatcher(path, interval, cb)
}
func NewConfigDirWatcher(dir string, interval time.Duration, cb func()) *FileWatcher {
	return impl.NewConfigDirWatcher(dir, interval, cb)
}
