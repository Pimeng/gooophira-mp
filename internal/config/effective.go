package config

import (
	"os"
	"path/filepath"
	"strings"
)

func boolOr(p *bool, d bool) bool {
	if p != nil {
		return *p
	}
	return d
}
func intOr(p *int, d int) int {
	if p != nil {
		return *p
	}
	return d
}
func strOr(p *string, d string) string {
	if p != nil {
		return *p
	}
	return d
}

func (c *ServerConfig) EffectiveMonitors() []int {
	if c.Monitors != nil {
		return c.Monitors
	}
	return DefaultMonitors
}
func (c *ServerConfig) EffectiveTestAccountIDs() []int {
	if c.TestAccountIDs != nil {
		return c.TestAccountIDs
	}
	return DefaultTestAccountIDs
}
func (c *ServerConfig) EffectiveServerName() string { return strOr(c.ServerName, DefaultServerName) }
func (c *ServerConfig) EffectiveHitokotoAPIURL() string {
	return strOr(c.HitokotoAPIURL, DefaultHitokotoAPIURL)
}
func (c *ServerConfig) EffectiveRoomMaxUsers() int { return intOr(c.RoomMaxUsers, DefaultRoomMaxUsers) }
func (c *ServerConfig) EffectiveRoomCreationEnabled() bool {
	return boolOr(c.RoomCreationEnabled, true)
}
func (c *ServerConfig) EffectivePlayingReconnectGrace() int {
	return intOr(c.PlayingReconnectGrace, DefaultPlayingReconnectGrace)
}
func (c *ServerConfig) EffectiveMaxRooms() int       { return intOr(c.MaxRooms, 0) }
func (c *ServerConfig) EffectiveMaxConnections() int { return intOr(c.MaxConnections, 0) }
func (c *ServerConfig) EffectiveConnectionRateLimit() int {
	return intOr(c.ConnectionRateLimit, DefaultConnectionRateLimit)
}
func (c *ServerConfig) EffectiveCommandRateLimit() bool { return boolOr(c.CommandRateLimit, true) }
func (c *ServerConfig) EffectiveHTTPRateLimitMaxRequests() int {
	return intOr(c.HTTPRateLimitMaxRequests, DefaultHTTPRateLimitMaxRequests)
}
func (c *ServerConfig) EffectiveHTTPRateLimitWindowMS() int {
	return intOr(c.HTTPRateLimitWindowMS, DefaultHTTPRateLimitWindowMS)
}
func (c *ServerConfig) EffectiveChatEnabled() bool   { return boolOr(c.ChatEnabled, true) }
func (c *ServerConfig) EffectiveReplayEnabled() bool { return boolOr(c.ReplayEnabled, false) }
func (c *ServerConfig) EffectiveReplayTTLDays() int {
	return intOr(c.ReplayTTLDays, DefaultReplayTTLDays)
}
func (c *ServerConfig) EffectiveReplayAutoUpload() bool { return boolOr(c.ReplayAutoUpload, false) }
func (c *ServerConfig) EffectiveSystemUserID() int      { return intOr(c.SystemUserID, 0) }
func (c *ServerConfig) EffectiveRoomListTip() string    { return strOr(c.RoomListTip, "") }
func (c *ServerConfig) EffectiveLogLevel() string       { return strOr(c.LogLevel, DefaultLogLevel) }
func (c *ServerConfig) EffectiveLogCompressAfterDays() int {
	return intOr(c.LogCompressAfterDays, DefaultLogCompressAfterDays)
}
func (c *ServerConfig) EffectiveLogMaxTotalMB() int {
	return intOr(c.LogMaxTotalMB, DefaultLogMaxTotalMB)
}
func (c *ServerConfig) EffectiveLang() string            { return strOr(c.Lang, "") }
func (c *ServerConfig) EffectiveRealIPHeader() string    { return strOr(c.RealIPHeader, "") }
func (c *ServerConfig) EffectiveHAProxyProtocol() bool   { return boolOr(c.HAProxyProtocol, false) }
func (c *ServerConfig) EffectiveHTTPService() bool       { return boolOr(c.HTTPService, false) }
func (c *ServerConfig) EffectiveGUI() bool               { return boolOr(c.GUI, false) }
func (c *ServerConfig) EffectiveAllowTokenInQuery() bool { return boolOr(c.AllowTokenInQuery, false) }
func (c *ServerConfig) EffectiveWebhook() *WebhookConfig { return c.Webhook }
func (c *ServerConfig) EffectiveStatsDBPath() string     { return strOr(c.StatsDBPath, "stats.db") }
func (c *ServerConfig) EffectiveStatsDetailRetentionDays() int {
	return intOr(c.StatsDetailRetentionDays, DefaultStatsDetailRetentionDays)
}
func (c *ServerConfig) EffectiveStatsDBMaxMB() int { return intOr(c.StatsDBMaxMB, DefaultStatsDBMaxMB) }
func (w *WebhookConfig) WebhookTimeoutMS() int {
	if w == nil || w.TimeoutMS <= 0 {
		return DefaultWebhookTimeoutMS
	}
	return w.TimeoutMS
}
func (w *WebhookConfig) WebhookRetryCount() int {
	if w == nil || w.Retries < 0 {
		return DefaultWebhookRetries
	}
	return w.Retries
}
func (c *ServerConfig) EffectiveCorsOrigins() []string {
	if c.CorsOrigins != nil {
		return c.CorsOrigins
	}
	return []string{}
}
func (c *ServerConfig) EffectiveDNSServers() []string {
	if c.Netutil != nil && len(c.Netutil.DNSServers) > 0 {
		out := make([]string, 0, len(c.Netutil.DNSServers))
		for _, s := range c.Netutil.DNSServers {
			if t := strings.TrimSpace(s); t != "" {
				out = append(out, t)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return DefaultDNSServers
}
func (c *ServerConfig) EffectiveProxyURL() string {
	if c.OutboundProxy == nil || c.OutboundProxy.Direct {
		return ""
	}
	return c.OutboundProxy.URL
}
func (c *ServerConfig) ShareStationConfigured() bool {
	return c.ShareStation != nil && c.ShareStation.URL != "" && c.ShareStation.Token != ""
}
func (c *ServerConfig) EffectiveReplayBaseDir() string {
	if c.ReplayBaseDir != nil && *c.ReplayBaseDir != "" {
		return *c.ReplayBaseDir
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return filepath.Join(cwd, "record")
}
