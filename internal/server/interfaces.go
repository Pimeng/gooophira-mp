// Package server 是服务器核心域：全局状态 ServerState、房间 Room、用户 User 及其
// 游戏逻辑。这些类型相互引用紧密（state↔room↔user↔session），按 Go 惯例放在同一
// 包内以避免循环依赖；传输层（network）通过本包定义的接口反向接入，从而打破
// network↔server 的环。
//
// 框架阶段：类型与字段已定形，复杂方法体留 TODO: stage-3，保证 go build 通过。
package server

import "github.com/Pimeng/gooophira-mp/internal/protocol"

// Session 是「已连接客户端」对核心域暴露的最小视图。具体的 TCP 会话在 network 包
// 实现本接口，从而 ServerState/User 无需 import network（打破循环依赖）。
type Session interface {
	// ID 返回会话唯一标识。
	ID() string
	// TrySend 尝试向客户端发送一条命令；无活跃连接时静默忽略。
	TrySend(cmd protocol.ServerCommand)
	// TrySendFrame 尝试向客户端发送一条预编码的二进制帧（广播优化用）。
	// 实现应处理 frame 的所有权（需要则拷贝，不需要则复用）。
	TrySendFrame(frame []byte)
	// TrySendFrameOwned 尝试发送调用方拥有所有权的帧；不再拷贝。调用方须保证
	// frame 在 sendCh 读取前不会被复用或修改。
	TrySendFrameOwned(frame []byte)
	// Close 关闭会话。
	Close()
}

// Logger 是核心域使用的日志接口（具体实现见 Stage 5 的 logging 包）。
type Logger interface {
	Debug(msg string)
	Info(msg string)
	Mark(msg string)
	Warn(msg string)
	Error(msg string)
	// DebugEnabled 报告 DEBUG 级别是否启用（供帧热路径短路日志格式化）。
	DebugEnabled() bool
}

// WsBroadcaster 是房间/管理面板实时推送接口（WebSocket 服务实现）。
type WsBroadcaster interface {
	BroadcastRoomUpdate(roomID protocol.RoomID)
	BroadcastAdminUpdate()
}

// WebSocketService 是完整的 WebSocket 服务引用（超集，含广播能力）。
type WebSocketService interface {
	WsBroadcaster
}

// ReplayRecorder 是回放录制器接口（具体实现见 Stage 5 的 replay 包）。
type ReplayRecorder interface {
	SetBaseDir(dir string)
	// AppendTouches 追加某玩家的触摸帧到回放。
	AppendTouches(roomID protocol.RoomID, userID int, frames []protocol.TouchFrame)
	// AppendJudges 追加某玩家的判定事件到回放。
	AppendJudges(roomID protocol.RoomID, userID int, judges []protocol.JudgeEvent)
	// SetRecordID 记录某玩家本局成绩的 record id。
	SetRecordID(roomID protocol.RoomID, userID, recordID int)
	// EndRoom 结束并落盘某房间进行中的录制（回放热关闭时也会调用）。
	EndRoom(roomID protocol.RoomID)
	// FakeMonitorInfo 返回用于让客户端上报游戏数据的假观战者信息。
	FakeMonitorInfo(name string) protocol.UserInfo
}

// MonitorBuffer 聚合实时游戏数据（Touches/Judges）后批量转发给观战者，
// 避免高频帧直接冲击网络。具体实现 AggregatingMonitorBuffer 在本包 monitorbuffer.go，
// 按缓冲量动态 flush（10/20/50ms）；测试用 mock。
type MonitorBuffer interface {
	BufferTouches(room *Room, userID int, frames []protocol.TouchFrame)
	BufferJudges(room *Room, userID int, judges []protocol.JudgeEvent)
}

// ConsoleOutputLine 是 CLI/GUI 控制台命令执行后捕获的一行输出。
// Kind 决定 GUI 端配色（out | error | success | info），对齐 TS cliHelpers.ConsoleOutputLine。
type ConsoleOutputLine struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// TempAdminToken 是临时管理员 token 的元数据。
type TempAdminToken struct {
	IP        string
	ExpiresAt int64 // Unix 毫秒
	Banned    bool
}

// CLIApprovalStatus 是 CLI 提权批准会话的状态。
type CLIApprovalStatus string

// CLI 提权批准状态枚举。
const (
	CLIApprovalPending  CLIApprovalStatus = "pending"
	CLIApprovalApproved CLIApprovalStatus = "approved"
	CLIApprovalDenied   CLIApprovalStatus = "denied"
)

// CLIApprovalSession 是一次 CLI 提权批准会话。
type CLIApprovalSession struct {
	IP             string
	ExpiresAt      int64
	Status         CLIApprovalStatus
	Token          string
	TokenExpiresAt int64
	RequestedAt    int64
}

// AutoUploadConfig 控制 UI 是否显示上传按钮（仅内存）。
type AutoUploadConfig struct {
	Show bool
}

// UploadedReplayMeta 是已上传回放的元数据（去重用）。
type UploadedReplayMeta struct {
	ScoreID   int
	ChartID   int
	Timestamp int64
}

// 时长常量。
const (
	// TempTokenTTLMS 临时管理员 token 有效期（4 小时）。
	TempTokenTTLMS = 4 * 60 * 60 * 1000
	// OTPTTLMS OTP / CLI 提权会话有效期（1 分钟）。
	OTPTTLMS = 1 * 60 * 1000
)
