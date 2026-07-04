package server

import (
	"math"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

// DangleToken 是断线挂起的唯一标识。用指针身份比较，验证「重连是否来自同一断线
// 事件」（对应 TS 用空对象 `{}` 的身份比较）。
type DangleToken struct{}

// User 代表一个在线用户：基本信息、会话关联（支持断线重连）、房间关联、观战权限、
// 游戏时间跟踪与 dangling 状态。
//
// 并发安全分组（明确边界，避免 Go 与 Java 版混用锁时引入隐性数据竞争）：
//   - state.Mu（全局）：保护 Room 字段与 Monitor 字段的「跨命令」一致性读写。
//     所有 ProcessXxx 与 BuildXxx 路径都至少持 state.Mu。
//   - User.Mu（细粒度 RWMutex）：保护 Session、dangleToken、DangleDeadline。
//     写入只发生在 readLoop goroutine 或 cleanup 持 state.Mu 期间，读取分散在
//     dispatch（持 state.Mu）、notifyDanglingReconnect（持 state.Mu）等路径。
//   - gameTimeBits（atomic.Uint64）：GameTime 字段用 bit-cast 存储，Touches 热路径
//     无锁写；admin/build 路径原子读后 bit-cast 还原，避免被 room.Mu 锁外读取。
//   - infoCache（atomic.Pointer）：消除原版本「指针字段 + 普通赋值」造成的 race
//     detector 告警。Monitor 变化时由 ToInfo 重新构造。
type User struct {
	// ID 是 Phira 用户 ID。
	ID int
	// Name 是显示名称。
	Name string
	// Lang 是语言偏好。
	Lang *l10n.Language
	// Server 是全局状态引用。
	Server *ServerState

	// Mu 保护 Session、dangleToken、DangleDeadline 的并发访问。
	Mu sync.RWMutex

	// Session 是当前关联会话（nil = 离线/断线）。
	Session Session
	// Room 是当前所在房间（nil = 不在任何房间）。由 state.Mu 保护。
	Room *Room
	// Monitor 标记是否为观战者。由 state.Mu 保护。
	Monitor bool

	// infoCache 是协议层 UserInfo 的原子缓存（指针语义，nil = 尚未构造）。
	// Monitor 变化或缓存为空时由 ToInfo 重新填充。
	infoCache atomic.Pointer[protocol.UserInfo]

	// gameTimeBits 是 GameTime（float64）的 bit-cast 存储。读取用 math.Float64frombits
	// 还原。atomic 写避免与 admin 视图路径形成交叉锁。
	gameTimeBits atomic.Uint64

	dangleToken *DangleToken
	// DangleDeadline 是断线挂起截止时间（Unix 毫秒）；nil = 当前未挂起。
	DangleDeadline *int64
}

// NewUser 创建用户实例。
func NewUser(id int, name, language string, server *ServerState) *User {
	u := &User{
		ID:     id,
		Name:   name,
		Lang:   l10n.NewLanguage(language),
		Server: server,
	}
	u.SetGameTime(math.Inf(-1))
	return u
}

// GameTime 返回当前游戏时间（-Inf 表示尚未开始）。
func (u *User) GameTime() float64 {
	return math.Float64frombits(u.gameTimeBits.Load())
}

// SetGameTime 原子写入当前游戏时间。Touches 热路径调用，无需加锁。
func (u *User) SetGameTime(t float64) {
	u.gameTimeBits.Store(math.Float64bits(t))
}

// ToInfo 返回用于协议传输的 UserInfo。Monitor 通过 state.Mu 保护——调用方须持 state.Mu。
// infoCache 用 atomic.Pointer 缓存上一次构造结果；Monitor 变化时丢弃缓存重建。
func (u *User) ToInfo() protocol.UserInfo {
	info := u.infoCache.Load()
	if info != nil && info.Monitor == u.Monitor {
		return *info
	}
	out := &protocol.UserInfo{ID: int32FromInt(u.ID), Name: u.Name, Monitor: u.Monitor}
	u.infoCache.Store(out)
	return *out
}

// CanMonitor 报告用户是否在 monitors 配置列表中（有观战权限）。
func (u *User) CanMonitor() bool {
	return slices.Contains(u.Server.Config.EffectiveMonitors(), u.ID)
}

// SetSession 设置/清除关联会话；设置新会话时清除 dangling 状态。
func (u *User) SetSession(session Session) {
	u.Mu.Lock()
	u.Session = session
	u.dangleToken = nil
	u.DangleDeadline = nil
	u.Mu.Unlock()
}

// TrySend 尝试向用户发送命令；无活跃会话时静默忽略。
func (u *User) TrySend(cmd protocol.ServerCommand) {
	u.Mu.RLock()
	s := u.Session
	u.Mu.RUnlock()
	if s == nil {
		return
	}
	s.TrySend(cmd)
}

// TrySendFrame 尝试向用户发送预编码的二进制帧；无活跃会话时静默忽略。
func (u *User) TrySendFrame(frame []byte) {
	u.Mu.RLock()
	s := u.Session
	u.Mu.RUnlock()
	if s == nil {
		return
	}
	s.TrySendFrame(frame)
}

// TrySendFrameOwned 同 TrySendFrame，但假设 frame 由调用方拥有所有权，不再拷贝。
func (u *User) TrySendFrameOwned(frame []byte) {
	u.Mu.RLock()
	s := u.Session
	u.Mu.RUnlock()
	if s == nil {
		return
	}
	s.TrySendFrameOwned(frame)
}

// MarkDangle 标记用户为 dangling（断线等待重连），返回用于校验重连的 token。
// deadlineMs 是 Unix 毫秒的挂起截止时间（用于播报「等待重连」剩余秒数），
// nil 表示不显示倒计时（极短宽限等场景）。须由调用方持 state.Mu。
func (u *User) MarkDangle(deadlineMs *int64) *DangleToken {
	u.Mu.Lock()
	token := &DangleToken{}
	u.dangleToken = token
	u.DangleDeadline = deadlineMs
	u.Mu.Unlock()
	return token
}

// IsStillDangling 报告用户是否仍处于由 token 标识的 dangling 状态。
// 须由调用方持 state.Mu。
func (u *User) IsStillDangling(token *DangleToken) bool {
	u.Mu.RLock()
	same := u.dangleToken == token
	u.Mu.RUnlock()
	return same
}

// IsConnected 报告用户当前是否有活跃会话（true = 在线，false = 离线/挂起）。
// Session 由 User.Mu 保护，调用方无需持 state.Mu。
func (u *User) IsConnected() bool {
	u.Mu.RLock()
	connected := u.Session != nil
	u.Mu.RUnlock()
	return connected
}

// dangleDeadlineMs 返回挂起剩余毫秒数（0 = 无挂起或已到期），用于播报「等待重连」。
// 须由调用方持 state.Mu。
func (u *User) dangleDeadlineMs(nowMs int64) int64 {
	u.Mu.RLock()
	defer u.Mu.RUnlock()
	if u.DangleDeadline == nil {
		return 0
	}
	remain := *u.DangleDeadline - nowMs
	if remain < 0 {
		return 0
	}
	return remain
}

// int32FromInt 安全地将 int 转换为 int32（带溢出检查，满足 CodeQL 边界验证要求）。
// 用于所有已知安全的 int→int32 窄化场景（用户 ID、谱面 ID 等数据库来源的整型）。
func int32FromInt(n int) int32 {
	if n < int(math.MinInt32) || n > int(math.MaxInt32) {
		panic("integer overflow: value does not fit in int32")
	}
	return int32(n)
}

// TL 是 l10n.TL 针对该用户语言的便捷封装。
func (u *User) TL(key string, args map[string]string) string {
	return l10n.TL(u.Lang, key, args)
}
