package server

import "time"

// EventType 是服务器对外事件的类型（与 Webhook 订阅过滤、载荷里的 type 字段一致）。
type EventType string

// 事件类型枚举。新增类型时同步更新 config.example/agent.yaml 和 Webhook 迁移参考。
const (
	EventGameStart      EventType = "game_start"      // 房间进入游戏
	EventGameEnd        EventType = "game_end"        // 一局结束
	EventScoreSubmitted EventType = "score_submitted" // 玩家提交成绩（飞书流式更新用）
	EventRoomCreate     EventType = "room_create"     // 新建房间
	EventRoomDisband    EventType = "room_disband"    // 房间解散
	EventUserJoin       EventType = "user_join"       // 用户加入房间
	EventMaintenance    EventType = "maintenance"     // 维护模式切换
)

// Event 是一条服务器事件的结构化载荷。字段按事件类型选填（如 maintenance 用 Enabled/Message，
// 房间类用 RoomID，对局类附 Chart*）。供 EventSink 异步外发（Webhook 等）。
//
// 飞书交互式模板相关字段（ChartDifficulty/ChartCharter/PlayerList/ImageURL）由事件
// 发射方按需填充：投递到飞书开放平台时，ImageURL 指向的图片会被下载并经飞书上传图片
// 接口换取 image_key，再填入模板变量 chart_pic；其余字段按名映射到模板变量。
type Event struct {
	Type      EventType `json:"type"`
	Time      time.Time `json:"time"`
	Server    string    `json:"server"`
	RoomID    string    `json:"room_id,omitempty"`
	ChartID   int       `json:"chart_id,omitempty"`
	ChartName string    `json:"chart_name,omitempty"`
	UserID    int       `json:"user_id,omitempty"`
	UserName  string    `json:"user_name,omitempty"`
	UserCount int       `json:"user_count,omitempty"`
	Enabled   bool      `json:"enabled,omitempty"` // maintenance 开/关
	Message   string    `json:"message,omitempty"` // maintenance 自定义提示等

	// 飞书交互式模板变量（仅 Type=feishu 的目标会消费）。
	ChartDifficulty string `json:"chart_difficulty,omitempty"` // 谱面难度（如 "AT. 14"）
	ChartCharter    string `json:"chart_charter,omitempty"`    // 谱师
	PlayerList      string `json:"player_list,omitempty"`      // 玩家列表（已格式化的字符串）
	ImageURL        string `json:"image_url,omitempty"`        // 谱面预览图 URL；投递时下载并上传飞书换 image_key

	// game_end 事件的成绩排行（按 score 降序）。飞书模板变量 player_score_rank 消费此切片。
	PlayerScoreRank []ScoreRankEntry `json:"player_score_rank,omitempty"`
	Players         []EventPlayer    `json:"players,omitempty"`
}

type EventPlayer struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// ScoreRankEntry 是单条房间成绩排行数据，用于 game_end 事件的 player_score_rank 模板变量。
type ScoreRankEntry struct {
	PlayerID int     `json:"player_id"`
	Player   string  `json:"player"`    // 玩家昵称
	Score    int     `json:"score"`     // 成绩
	StdScore float64 `json:"std_score"` // 标准分
}

// EventSink 接收服务器事件用于异步外发。
//
// 约定：Emit 通常在持有 ServerState.Mu 时被调用，实现**必须非阻塞**（队列满即丢弃，
// 真正的网络 I/O 放到自有 goroutine、锁外完成），绝不可阻塞命令处理。
type EventSink interface {
	Emit(ev Event)
}
