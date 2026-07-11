package config

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// 内置默认值（对应 TS 各使用点的 `config.x ?? default`）。集中在此，唯一来源。
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
)

// 列表类默认值。
var (
	DefaultMonitors       = []int{2}
	DefaultTestAccountIDs = []int{1739989}
	DefaultDNSServers     = []string{"1.1.1.1:53", "8.8.8.8:53"}
)

// ServerConfig 是服务器配置。可选标量用指针（nil = 未设置），通过 Effective* 方法
// 落地默认值。字段名对应 TS 的 snake_case key，注释标注其 ENV/YAML 名。
type ServerConfig struct {
	Monitors                 []int          // MONITORS
	TestAccountIDs           []int          // TEST_ACCOUNT_IDS
	ServerName               *string        // SERVER_NAME
	Host                     *string        // HOST (startup-only)
	Port                     *int           // PORT (startup-only)
	HTTPService              *bool          // HTTP_SERVICE (startup-only)
	HTTPPort                 *int           // HTTP_PORT (startup-only)
	GUI                      *bool          // GUI (startup-only)
	RoomMaxUsers             *int           // ROOM_MAX_USERS
	RoomCreationEnabled      *bool          // ROOM_CREATION_ENABLED
	PlayingReconnectGrace    *int           // PLAYING_RECONNECT_GRACE
	MaxRooms                 *int           // MAX_ROOMS
	MaxConnections           *int           // MAX_CONNECTIONS
	ConnectionRateLimit      *int           // CONNECTION_RATE_LIMIT
	CommandRateLimit         *bool          // COMMAND_RATE_LIMIT
	HTTPRateLimitMaxRequests *int           // HTTP_RATE_LIMIT_MAX_REQUESTS
	HTTPRateLimitWindowMS    *int           // HTTP_RATE_LIMIT_WINDOW_MS
	ChatEnabled              *bool          // CHAT_ENABLED
	ReplayEnabled            *bool          // REPLAY_ENABLED
	ReplayBaseDir            *string        // REPLAY_BASE_DIR
	ReplayTTLDays            *int           // REPLAY_TTL_DAYS
	ReplayAutoUpload         *bool          // REPLAY_AUTO_UPLOAD
	SystemUserID             *int           // SYSTEM_USER_ID
	AdminToken               *string        // ADMIN_TOKEN
	AdminDataPath            *string        // ADMIN_DATA_PATH (startup-only)
	RoomListTip              *string        // ROOM_LIST_TIP
	LogLevel                 *string        // LOG_LEVEL
	LogCompressAfterDays     *int           // LOG_COMPRESS_AFTER_DAYS
	LogMaxTotalMB            *int           // LOG_MAX_TOTAL_MB
	RealIPHeader             *string        // REAL_IP_HEADER
	CorsOrigins              []string       // CORS_ORIGINS
	HAProxyProtocol          *bool          // HAPROXY_PROTOCOL
	Lang                     *string        // LANG (PHIRA_MP_LANG 优先)
	PhiraAPIEndpoint         *string        // PHIRA_API_ENDPOINT
	OutboundProxy            *OutboundProxy // OUTBOUND_PROXY
	Netutil                  *NetutilConfig // NETUTIL
	ShareStation             *ShareStation  // SHARE_STATION
	Redis                    *RedisConfig   // REDIS (startup-only)
	HitokotoAPIURL           *string        // HITOKOTO_API_URL
	AllowTokenInQuery        *bool          // ALLOW_TOKEN_IN_QUERY
	Webhook                  *WebhookConfig // WEBHOOK
	StatsDBPath              *string        // STATS_DB_PATH
	StatsDetailRetentionDays *int           // STATS_DETAIL_RETENTION_DAYS
	StatsDBMaxMB             *int           // STATS_DB_MAX_MB
}

// ---------- Effective* 访问器：未设置时落地默认值（唯一来源） ----------

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

func (c *ServerConfig) EffectiveServerName() string {
	return strOr(c.ServerName, DefaultServerName)
}

// EffectiveHitokotoAPIURL 返回一言 API 地址：已配置且非空则用之，否则用内置默认。
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
func (c *ServerConfig) EffectiveReplayAutoUpload() bool {
	return boolOr(c.ReplayAutoUpload, false)
}

// EffectiveSystemUserID 返回系统消息发送者使用的用户 ID。
// 未配置或 <=0 时返回 0（保留「系统」语义，客户端按系统消息渲染）。
// 配置为真实 Phira 用户 ID 后，客户端会凭此 ID 向 Phira 拉取该用户的头像与昵称，
// 让所有系统消息（欢迎语、回放/迟到提示、管理员广播、维护通知、本局结算等）与
// 回放假观战者共用同一身份，呈现为真实用户外观而非匿名系统。
func (c *ServerConfig) EffectiveSystemUserID() int {
	return intOr(c.SystemUserID, 0)
}
func (c *ServerConfig) EffectiveRoomListTip() string { return strOr(c.RoomListTip, "") }
func (c *ServerConfig) EffectiveLogLevel() string    { return strOr(c.LogLevel, DefaultLogLevel) }
func (c *ServerConfig) EffectiveLogCompressAfterDays() int {
	return intOr(c.LogCompressAfterDays, DefaultLogCompressAfterDays)
}
func (c *ServerConfig) EffectiveLogMaxTotalMB() int {
	return intOr(c.LogMaxTotalMB, DefaultLogMaxTotalMB)
}
func (c *ServerConfig) EffectiveLang() string { return strOr(c.Lang, "") }
func (c *ServerConfig) EffectiveRealIPHeader() string {
	return strOr(c.RealIPHeader, "")
}
func (c *ServerConfig) EffectiveHAProxyProtocol() bool   { return boolOr(c.HAProxyProtocol, false) }
func (c *ServerConfig) EffectiveHTTPService() bool       { return boolOr(c.HTTPService, false) }
func (c *ServerConfig) EffectiveGUI() bool               { return boolOr(c.GUI, false) }
func (c *ServerConfig) EffectiveAllowTokenInQuery() bool { return boolOr(c.AllowTokenInQuery, false) }

// EffectiveWebhook 返回 Webhook 配置（未设置时为 nil = 关闭）。
func (c *ServerConfig) EffectiveWebhook() *WebhookConfig { return c.Webhook }

func (c *ServerConfig) EffectiveStatsDBPath() string {
	return strOr(c.StatsDBPath, "stats.db")
}
func (c *ServerConfig) EffectiveStatsDetailRetentionDays() int {
	return intOr(c.StatsDetailRetentionDays, DefaultStatsDetailRetentionDays)
}
func (c *ServerConfig) EffectiveStatsDBMaxMB() int {
	return intOr(c.StatsDBMaxMB, DefaultStatsDBMaxMB)
}

// WebhookTimeoutMS 返回单次请求超时（落地默认值）。
func (w *WebhookConfig) WebhookTimeoutMS() int {
	if w == nil || w.TimeoutMS <= 0 {
		return DefaultWebhookTimeoutMS
	}
	return w.TimeoutMS
}

// WebhookRetryCount 返回失败重试次数（落地默认值）。
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

// EffectiveDNSServers 返回 Android 平台公共 DNS 服务器列表：NETUTIL.DNS_SERVERS
// 配置有效则用之，否则回退内置默认。
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

// EffectiveProxyURL 返回出站代理 URL（空 = 未配置，走环境变量）。
// OUTBOUND_PROXY 为 false（Direct=true）时返回空字符串表示直连。
func (c *ServerConfig) EffectiveProxyURL() string {
	if c.OutboundProxy == nil || c.OutboundProxy.Direct {
		return ""
	}
	return c.OutboundProxy.URL
}

// ShareStationConfigured 报告分享站是否已完整配置（URL 与 Token 均非空）。
func (c *ServerConfig) ShareStationConfigured() bool {
	return c.ShareStation != nil && c.ShareStation.URL != "" && c.ShareStation.Token != ""
}

// EffectiveReplayBaseDir 返回回放录制基础目录：已配置则用之，否则用 <cwd>/record
// （对应 TS 的 defaultReplayBaseDir）。
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

// ---------- 解析助手（接受 env 字符串或 YAML 原生类型） ----------

func asFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

func asInt(v any) (int, bool) {
	f, ok := asFloat(v)
	if !ok || f != math.Trunc(f) {
		return 0, false
	}
	return int(f), true
}

func parseBoolValue(v any) (bool, bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case int:
		if x == 1 {
			return true, true
		}
		if x == 0 {
			return false, true
		}
		return false, false
	case float64:
		if x == 1 {
			return true, true
		}
		if x == 0 {
			return false, true
		}
		return false, false
	case string:
		s := strings.ToLower(strings.TrimSpace(x))
		switch s {
		case "1", "true", "yes", "on":
			return true, true
		case "0", "false", "no", "off":
			return false, true
		}
		return false, false
	default:
		return false, false
	}
}

func parseStringValue(v any) (string, bool) {
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	t := strings.TrimSpace(s)
	if t == "" {
		return "", false
	}
	return t, true
}

func parsePortValue(v any) (int, bool) {
	n, ok := asInt(v)
	if !ok || n <= 0 || n > 65535 {
		return 0, false
	}
	return n, true
}

// logLevels 接受 4 字符规范名（DEBU/INFO/MARK/WARN/ERRO）与 5 字符旧别名
// （DEBUG/ERROR），统一规范化为短形式后落盘与下发，详见 parseLogLevelValue。
var logLevels = map[string]string{
	"DEBU":  "DEBU",
	"DEBUG": "DEBU",
	"INFO":  "INFO",
	"MARK":  "MARK",
	"WARN":  "WARN",
	"ERRO":  "ERRO",
	"ERROR": "ERRO",
}

func parseLogLevelValue(v any) (string, bool) {
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	u := strings.ToUpper(strings.TrimSpace(s))
	norm, ok := logLevels[u]
	if u == "" || !ok {
		return "", false
	}
	return norm, true
}

func parseRoomMaxUsersValue(v any) (int, bool) {
	n, ok := asInt(v)
	if !ok || n < 1 {
		return 0, false
	}
	return min(n, MaxRoomMaxUsers), true
}

func parseReplayTTLDaysValue(v any) (int, bool) {
	n, ok := asInt(v)
	if !ok || n < 1 {
		return 0, false
	}
	return min(n, MaxReplayTTLDays), true
}

func parsePositiveIntValue(v any) (int, bool) {
	n, ok := asInt(v)
	if !ok || n < 1 {
		return 0, false
	}
	return n, true
}

func parseNonNegativeIntValue(v any) (int, bool) {
	n, ok := asInt(v)
	if !ok || n < 0 {
		return 0, false
	}
	return n, true
}

func parsePlayingGraceValue(v any) (int, bool) {
	n, ok := parseNonNegativeIntValue(v)
	if !ok {
		return 0, false
	}
	return min(n, MaxPlayingReconnectGrace), true
}

var listSepInt = regexp.MustCompile(`[,\s;，]+`)
var listSepStr = regexp.MustCompile(`[,\s;]+`)

func parseIntegerListValue(v any) ([]int, bool) {
	switch x := v.(type) {
	case []any:
		out := make([]int, 0, len(x))
		for _, it := range x {
			if n, ok := asInt(it); ok {
				out = append(out, n)
			}
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, true
	case []int:
		if len(x) == 0 {
			return nil, false
		}
		return x, true
	case string:
		if strings.TrimSpace(x) == "" {
			return nil, false
		}
		var out []int
		for _, part := range listSepInt.Split(x, -1) {
			if n, ok := asInt(strings.TrimSpace(part)); ok {
				out = append(out, n)
			}
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, true
	default:
		if n, ok := asInt(v); ok {
			return []int{n}, true
		}
		return nil, false
	}
}

func parseStringListValue(v any) ([]string, bool) {
	switch x := v.(type) {
	case []any:
		var out []string
		for _, it := range x {
			if s, ok := it.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, true
	case []string:
		var out []string
		for _, s := range x {
			if strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, true
	case string:
		if strings.TrimSpace(x) == "" {
			return nil, false
		}
		var out []string
		for _, part := range listSepStr.Split(x, -1) {
			if t := strings.TrimSpace(part); t != "" {
				out = append(out, t)
			}
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, true
	default:
		return nil, false
	}
}

func parseOutboundProxyValue(v any) (*OutboundProxy, bool) {
	switch x := v.(type) {
	case bool:
		if !x {
			return &OutboundProxy{Direct: true}, true
		}
		return nil, false
	case string:
		t := strings.TrimSpace(x)
		if t == "" {
			return nil, false
		}
		if strings.ToLower(t) == "false" {
			return &OutboundProxy{Direct: true}, true
		}
		return &OutboundProxy{URL: t}, true
	default:
		return nil, false
	}
}

func asRecord(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func parseShareStationValue(v any) (*ShareStation, bool) {
	m, ok := asRecord(v)
	if !ok {
		return nil, false
	}
	url, okURL := parseStringValue(m["URL"])
	token, okTok := parseStringValue(m["TOKEN"])
	if !okURL || !okTok {
		return nil, false
	}
	return &ShareStation{URL: url, Token: token}, true
}

func parseRedisValue(v any) (*RedisConfig, bool) {
	m, ok := asRecord(v)
	if !ok {
		return nil, false
	}
	enabled, okEn := parseBoolValue(m["ENABLED"])
	if !okEn {
		return nil, false
	}
	host, okHost := parseStringValue(m["HOST"])
	if !okHost {
		host = "127.0.0.1"
	}
	port, okPort := parsePortValue(m["PORT"])
	if !okPort {
		port = 6379
	}
	password, _ := parseStringValue(m["PASSWORD"])
	db := 0
	if raw, present := m["DB"]; present {
		if n, ok := asInt(raw); ok && n >= 0 {
			db = n
		} else if raw != nil && raw != "" {
			return nil, false
		}
	}
	return &RedisConfig{Enabled: enabled, Host: host, Port: port, Password: password, DB: db}, true
}

// parseNetutilValue 解析 NETUTIL 块。仅当 v 根本不是 map 时返回 false（视为未设置）。
// DNS_SERVERS 为空或全空白时仍返回结构体，但 EffectiveDNSServers 会回退到内置默认。
func parseNetutilValue(v any) (*NetutilConfig, bool) {
	m, ok := asRecord(v)
	if !ok {
		return nil, false
	}
	servers, _ := parseStringListValue(m["DNS_SERVERS"])
	return &NetutilConfig{DNSServers: servers}, true
}

// parseWebhookValue 解析 WEBHOOK 块。结构合法即返回（即便 ENABLED 缺省为 false / TARGETS 为空），
// 仅当 v 根本不是 map 时返回 false（视为未设置）。逐个目标解析：缺 URL 的目标跳过。
func parseWebhookValue(v any) (*WebhookConfig, bool) {
	m, ok := asRecord(v)
	if !ok {
		return nil, false
	}
	enabled, _ := parseBoolValue(m["ENABLED"]) // 缺省/非法 → false（显式 opt-in）
	timeoutMS := 0
	if n, ok := asInt(m["TIMEOUT_MS"]); ok && n > 0 {
		timeoutMS = n
	}
	retries := -1
	if n, ok := asInt(m["RETRIES"]); ok && n >= 0 {
		retries = n
	}

	var targets []WebhookTarget
	if rawList, present := m["TARGETS"]; present {
		if list, ok := rawList.([]any); ok {
			for _, item := range list {
				tm, ok := asRecord(item)
				if !ok {
					continue
				}
				url, okURL := parseStringValue(tm["URL"])
				if !okURL {
					continue // 无效目标：缺 URL，跳过
				}
				typ, _ := parseStringValue(tm["TYPE"])
				typ = strings.ToLower(typ)
				if typ == "" {
					typ = "generic"
				}
				events, _ := parseStringListValue(tm["EVENTS"]) // nil = 订阅全部
				secret, _ := parseStringValue(tm["SECRET"])
				targets = append(targets, WebhookTarget{URL: url, Type: typ, Events: events, Secret: secret})
			}
		}
	}

	return &WebhookConfig{Enabled: enabled, TimeoutMS: timeoutMS, Retries: retries, Targets: targets}, true
}
