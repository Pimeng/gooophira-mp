package model

type WebhookTarget struct {
	ID     string   // Agent 幂等账本中的稳定目标 ID；省略时从目标配置派生
	URL    string   // 投递地址（Type=generic/discord/onebot_v11 使用；feishu 不用）
	Type   string   // 目标类型：generic | discord | onebot_v11 | feishu（未知按 generic）
	Events []string // 订阅的事件类型；空 = 订阅全部
	Secret string   // 可选：HMAC-SHA256 签名密钥（写入 X-Phira-Signature 头；仅 HTTP 目标用）

	// OneBot v11 HTTP API 字段（仅 Type=onebot_v11 使用）。
	AccessToken string  // 可选：OneBot access token（Authorization: Bearer ...）
	MessageType string  // 消息目标类型：private | group
	TargetID    int64   // 私聊 QQ 号或群号；单值配置及单次投递使用
	TargetIDs   []int64 // TARGET_ID 为数组时的私聊 QQ 号或群号列表

	// 飞书开放平台 SDK 字段（仅 Type=feishu 使用）。
	AppID                  string // 应用 App ID
	AppSecret              string // 应用 App Secret
	ReceiveOpenID          string // 接收人 open_id
	TemplateID             string // 可选：覆盖 game_start 事件的内置模板 ID
	TemplateVersion        string // 可选：覆盖 game_start 模板版本
	GameEndTemplateID      string // 可选：覆盖 game_end 事件的内置模板 ID
	GameEndTemplateVersion string // 可选：覆盖 game_end 模板版本
	LiveUpdate             bool   // 飞书成绩卡片流式更新：首个成绩发送卡片，后续 PATCH 实时刷新
}

// Subscribes 报告该目标是否订阅了给定事件类型（空订阅列表视为订阅全部）。
// score_submitted 是飞书实时卡片的内部事件，仅投递到启用 LiveUpdate 的飞书目标；
// 这类目标也自动接收 game_end 和 room_disband，以完成最终更新并清理状态。

type WebhookConfig struct {
	Enabled   bool
	TimeoutMS int // 单次请求超时（ms），≤0 用默认
	Retries   int // 失败重试次数，<0 用默认
	Targets   []WebhookTarget
}

// RedisConfig 是 Redis 缓存配置。
