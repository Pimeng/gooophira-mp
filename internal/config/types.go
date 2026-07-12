// Package config 定义服务器配置类型、默认值与加载/合并/比对逻辑。
//
// ⚠️ Go 零值陷阱：原 TS 的 ServerConfig 字段多为可选，未设置时在「使用点」回退到
// 非零默认值（如 room_max_users→8、replay_ttl_days→4、chat_enabled→true）。Go 中
// bool 零值是 false、int 是 0，无法区分「未设置」与「显式设零」。因此这里所有可选
// 标量字段用指针（nil = 未设置），并通过 Effective* 方法集中落地默认值——既保留
// 持久化/差异比对所需的「存在性」，又把每个默认值写在唯一位置，避免散落出错。
package config

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
	GUI                      *bool
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
}

// ShareStation 是回放分享站配置（自动上传到第三方平台用）。
type ShareStation struct {
	URL   string
	Token string
}

// WebhookTarget 是单个 Webhook 投递目标。
//
// 对 Type=generic/discord 的目标：经 URL 出站 HTTP POST（载荷由 Format 生成）。
// 对 Type=feishu 的目标：经飞书开放平台 SDK 投递交互式模板消息，URL 不再使用，
// 改由 AppID/AppSecret 创建客户端、ReceiveOpenID 指定接收人；
// 模板 ID 可选覆盖（TemplateID 覆盖 game_start、GameEndTemplateID 覆盖 game_end），
// 留空时走飞书适配器内置常量（含硬编码版本号），事件字段（含可选 ImageURL 上传后换回的 image_key）映射到模板变量。
type WebhookTarget struct {
	URL    string   // 投递地址（仅 Type=generic/discord 经 HTTP POST；feishu 不用）
	Type   string   // 载荷格式：generic | discord | feishu（未知按 generic）
	Events []string // 订阅的事件类型；空 = 订阅全部
	Secret string   // 可选：HMAC-SHA256 签名密钥（写入 X-Phira-Signature 头；仅 HTTP 目标用）

	// 飞书开放平台 SDK 字段（仅 Type=feishu 使用）。
	AppID             string // 应用 App ID
	AppSecret         string // 应用 App Secret
	ReceiveOpenID     string // 接收人 open_id
	TemplateID        string // 可选：覆盖 game_start 事件的内置模板 ID；空 = 用内置默认
	GameEndTemplateID string // 可选：覆盖 game_end 事件的内置模板 ID；空 = 用内置默认
}

// Subscribes 报告该目标是否订阅了给定事件类型（空订阅列表视为订阅全部）。
func (t WebhookTarget) Subscribes(event string) bool {
	return len(t.Events) == 0 || slices.Contains(t.Events, event)
}

// WebhookConfig 是 Webhook 通知配置（对局/房间/维护等事件外发到群机器人等）。
type WebhookConfig struct {
	Enabled   bool
	TimeoutMS int // 单次请求超时（ms），≤0 用默认
	Retries   int // 失败重试次数，<0 用默认
	Targets   []WebhookTarget
}

// RedisConfig 是 Redis 缓存配置。
type RedisConfig struct {
	Enabled  bool
	Host     string // 默认 127.0.0.1
	Port     int    // 默认 6379
	Password string
	DB       int // 默认 0
}

// OutboundProxy 表示出站代理三态配置。
// 对应 TS 的 string | false | undefined：
//   - 字段为 nil 指针 → 未设置（undefined）
//   - Direct == true  → 强制直连（TS 的 false）
//   - URL 非空        → 使用该代理
type OutboundProxy struct {
	Direct bool
	URL    string
}

// NetutilConfig 是出站 HTTP 网络层配置（主要影响 Android/Termux 等无本地 stub resolver 环境）。
type NetutilConfig struct {
	DNSServers []string // Android 平台公共 DNS 服务器列表（含端口，如 "1.1.1.1:53"）；空 = 使用内置默认
}

// Chart 是谱面的最小信息。
// Chart 是谱面基本信息（/chart/:id 返回字段的子集）。
//
// 服务器仅依赖 ID/Name（处理选谱、广播、回放录制）；Level/Charter/Illustration
// 供飞书 Webhook 模板渲染（chart_difficulty/chart_charter/chart_pic）使用——
// Illustration 为封面图 URL，投递到飞书时下载并经飞书上传图片接口换取 image_key。
// 难度展示取 Level（如 "IN Lv.15"），而非数值 Difficulty（详见 Phira chart 信息格式文档）。
type Chart struct {
	ID   int
	Name string

	// Level 是谱面难度等级展示字符串（如 "IN Lv.15"）。对应 API 字段 level。
	Level string
	// Charter 是谱师署名。对应 API 字段 charter。
	Charter string
	// Illustration 是谱面封面图 URL（指向 phira.5wyxi.com/files/...）。
	// 飞书模板变量 chart_pic 由该 URL 下载后上传飞书换 image_key 填入。
	Illustration string
}

// RecordData 是 Phira API /record/:id 返回的成绩数据。
//
// Chart 用指针：旧客户端/API 未返回谱面 ID 时为 nil，此时跳过「成绩谱面是否与房间
// 当前谱面一致」的校验（fail-open，避免误伤正常玩家）。
type RecordData struct {
	ID        int
	Player    int
	Chart     *int // 该成绩对应的谱面 ID；nil = API 未返回，跳过校验
	Score     int
	Perfect   int
	Good      int
	Bad       int
	Miss      int
	MaxCombo  int
	Accuracy  float64
	Mod       int
	FullCombo bool
	Std       *float64 // nil => JSON 里的 null
	StdScore  *float64 // nil => JSON 里的 null
}
