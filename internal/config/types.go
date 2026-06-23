// Package config 定义服务器配置类型、默认值与加载/合并/比对逻辑。
//
// ⚠️ Go 零值陷阱：原 TS 的 ServerConfig 字段多为可选，未设置时在「使用点」回退到
// 非零默认值（如 room_max_users→8、replay_ttl_days→4、chat_enabled→true）。Go 中
// bool 零值是 false、int 是 0，无法区分「未设置」与「显式设零」。因此这里所有可选
// 标量字段用指针（nil = 未设置），并通过 Effective* 方法集中落地默认值——既保留
// 持久化/差异比对所需的「存在性」，又把每个默认值写在唯一位置，避免散落出错。
package config

// ShareStation 是回放分享站配置（自动上传到第三方平台用）。
type ShareStation struct {
	URL   string
	Token string
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

// Chart 是谱面的最小信息。
type Chart struct {
	ID   int
	Name string
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
	FullCombo bool
	Std       float64
	StdScore  float64
}
