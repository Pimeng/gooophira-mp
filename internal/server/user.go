package server

import (
	"math"
	"slices"

	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

// DangleToken 是断线挂起的唯一标识。用指针身份比较，验证「重连是否来自同一断线
// 事件」（对应 TS 用空对象 `{}` 的身份比较）。
type DangleToken struct{}

// User 代表一个在线用户：基本信息、会话关联（支持断线重连）、房间关联、观战权限、
// 游戏时间跟踪与 dangling 状态。
type User struct {
	// ID 是 Phira 用户 ID。
	ID int
	// Name 是显示名称。
	Name string
	// Lang 是语言偏好。
	Lang *l10n.Language
	// Server 是全局状态引用。
	Server *ServerState

	// Session 是当前关联会话（nil = 离线/断线）。
	Session Session
	// Room 是当前所在房间（nil = 不在任何房间）。
	Room *Room
	// Monitor 标记是否为观战者。
	Monitor bool

	infoCache *protocol.UserInfo

	// GameTime 是当前游戏时间（回放同步用），初始为 -Inf。
	GameTime float64

	dangleToken *DangleToken
	// DangleDeadline 是断线挂起截止时间（Unix 毫秒）；nil = 当前未挂起。
	DangleDeadline *int64
}

// NewUser 创建用户实例。
func NewUser(id int, name, language string, server *ServerState) *User {
	return &User{
		ID:       id,
		Name:     name,
		Lang:     l10n.NewLanguage(language),
		Server:   server,
		GameTime: math.Inf(-1),
	}
}

// ToInfo 返回用于协议传输的 UserInfo（带 monitor 变更感知的缓存）。
func (u *User) ToInfo() protocol.UserInfo {
	if u.infoCache == nil || u.infoCache.Monitor != u.Monitor {
		u.infoCache = &protocol.UserInfo{ID: int32(u.ID), Name: u.Name, Monitor: u.Monitor}
	}
	return *u.infoCache
}

// CanMonitor 报告用户是否在 monitors 配置列表中（有观战权限）。
func (u *User) CanMonitor() bool {
	return slices.Contains(u.Server.Config.EffectiveMonitors(), u.ID)
}

// SetSession 设置/清除关联会话；设置新会话时清除 dangling 状态。
func (u *User) SetSession(session Session) {
	u.Session = session
	u.dangleToken = nil
	u.DangleDeadline = nil
}

// TrySend 尝试向用户发送命令；无活跃会话时静默忽略。
func (u *User) TrySend(cmd protocol.ServerCommand) {
	if u.Session == nil {
		return
	}
	u.Session.TrySend(cmd)
}

// MarkDangle 标记用户为 dangling（断线等待重连），返回用于校验重连的 token。
func (u *User) MarkDangle() *DangleToken {
	token := &DangleToken{}
	u.dangleToken = token
	return token
}

// IsStillDangling 报告用户是否仍处于由 token 标识的 dangling 状态。
func (u *User) IsStillDangling(token *DangleToken) bool {
	return u.dangleToken == token
}

// TL 是 l10n.TL 针对该用户语言的便捷封装。
func (u *User) TL(key string, args map[string]string) string {
	return l10n.TL(u.Lang, key, args)
}
