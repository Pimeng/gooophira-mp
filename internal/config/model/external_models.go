package model

type ShareStation struct {
	URL   string
	Token string
}

// WebhookTarget 是单个 Webhook 投递目标。
//
// 对 Type=generic/discord 的目标：经 URL 出站 HTTP POST（载荷由 Format 生成）。
// 对 Type=onebot_v11 的目标：调用 OneBot v11 HTTP API 发送纯文本消息，URL 为 API
// 根地址，AccessToken 用于 Bearer 鉴权，MessageType/TargetID 指定私聊或群聊目标。
// 对 Type=feishu 的目标：经飞书开放平台 SDK 投递交互式模板消息，URL 不再使用，
// 改由 AppID/AppSecret 创建客户端、ReceiveOpenID 指定接收人；
// 模板 ID/版本可选覆盖（game_start 与 game_end 分别配置），留空时走飞书适配器
// 内置默认；事件字段（含可选 ImageURL 上传后换回的 image_key）映射到模板变量。

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
