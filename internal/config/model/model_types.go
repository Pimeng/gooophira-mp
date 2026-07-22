// Package config 定义服务器配置类型、默认值与加载/合并/比对逻辑。
//
// ⚠️ Go 零值陷阱：原 TS 的 ServerConfig 字段多为可选，未设置时在「使用点」回退到
// 非零默认值（如 room_max_users→8、replay_ttl_days→4、chat_enabled→true）。Go 中
// bool 零值是 false、int 是 0，无法区分「未设置」与「显式设零」。因此这里所有可选
// 标量字段用指针（nil = 未设置），并通过 Effective* 方法集中落地默认值——既保留
// 持久化/差异比对所需的「存在性」，又把每个默认值写在唯一位置，避免散落出错。
package model

import "slices"

// ServerConfig 是服务器配置。可选标量用指针（nil = 未设置），通过 Effective* 方法
// 落地默认值。字段名对应 TS 的 snake_case key，注释标注其 ENV/YAML 名。
type ServerConfig struct {
	Monitors                 []int
	TestAccountIDs           []int
	ServerName               *string
	Host                     *string
	Port                     *int
	HTTPService              *bool
	HTTPPort                 *int
	RoomMaxUsers             *int
	RoomCreationEnabled      *bool
	PlayingReconnectGrace    *int
	MaxRooms                 *int
	MaxConnections           *int
	ConnectionRateLimit      *int
	CommandRateLimit         *bool
	HTTPRateLimitMaxRequests *int
	HTTPRateLimitWindowMS    *int
	ChatEnabled              *bool
	ReplayEnabled            *bool
	ReplayBaseDir            *string
	ReplayTTLDays            *int
	ReplayAutoUpload         *bool
	SystemUserID             *int
	AdminToken               *string
	AdminDataPath            *string
	RoomListTip              *string
	LogLevel                 *string
	LogCompressAfterDays     *int
	LogMaxTotalMB            *int
	RealIPHeader             *string
	CorsOrigins              []string
	HAProxyProtocol          *bool
	Lang                     *string
	PhiraAPIEndpoint         *string
	OutboundProxy            *OutboundProxy
	Netutil                  *NetutilConfig
	ShareStation             *ShareStation
	Redis                    *RedisConfig
	HitokotoAPIURL           *string
	AllowTokenInQuery        *bool
	Webhook                  *WebhookConfig
	StatsDBPath              *string
	StatsDetailRetentionDays *int
	StatsDBMaxMB             *int
	AgentIPC                 *AgentIPCConfig
}

// AgentIPCConfig 管理可选本地 Agent 边界的服务端部分。
// 空 Token 会在启动时生成，并且只通过当前用户可读的发现文件公开。
func (t WebhookTarget) Subscribes(event string) bool {
	if event == "score_submitted" {
		return t.Type == "feishu" && t.LiveUpdate
	}
	if (event == "game_end" || event == "room_disband") && t.Type == "feishu" && t.LiveUpdate {
		return true
	}
	return len(t.Events) == 0 || slices.Contains(t.Events, event)
}

// WebhookConfig 是 Webhook 通知配置（对局/房间/维护等事件外发到群机器人等）。
