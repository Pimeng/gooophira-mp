package server

import "time"

// EventType 是服务器对外事件的类型（与 Webhook 订阅过滤、载荷里的 type 字段一致）。
type EventType string

// 事件类型枚举。新增类型时同步更新 server_config.example.yml 的 EVENTS 说明。
const (
	EventGameStart   EventType = "game_start"   // 房间进入游戏
	EventGameEnd     EventType = "game_end"     // 一局结束
	EventRoomCreate  EventType = "room_create"  // 新建房间
	EventRoomDisband EventType = "room_disband" // 房间解散
	EventUserJoin    EventType = "user_join"    // 用户加入房间
	EventMaintenance EventType = "maintenance"  // 维护模式切换
)

// Event 是一条服务器事件的结构化载荷。字段按事件类型选填（如 maintenance 用 Enabled/Message，
// 房间类用 RoomID，对局类附 Chart*）。供 EventSink 异步外发（Webhook 等）。
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
}

// EventSink 接收服务器事件用于异步外发。
//
// 约定：Emit 通常在持有 ServerState.Mu 时被调用，实现**必须非阻塞**（队列满即丢弃，
// 真正的网络 I/O 放到自有 goroutine、锁外完成），绝不可阻塞命令处理。
type EventSink interface {
	Emit(ev Event)
}
