package server

import (
	"github.com/Pimeng/gooophira-mp/internal/common/platform/l10n"
	"github.com/Pimeng/gooophira-mp/internal/common/protocol"
)

// 聊天内容最大长度（rune 计）。协议层已将 CmdChat.Message 截到 200 字节，
// 本常量主要兜底服务端拼装的 chat-disabled-by-server 等本地化文案，避免异常长串。
const maxChatLength = 500

func (h *Hub) localize(user *User, key string) string {
	return l10n.TL(user.Lang, key, nil)
}

// tlOrSkip 返回本地化文本；若 key 在 lang 中缺失（TL 返回 key 本身或空串）则 ok=false。
// 用于系统聊天提示这类「缺失即跳过」的场景，统一原本散落的 hint == "" || hint == key 检查。
func tlOrSkip(lang *l10n.Language, key string, args map[string]string) (text string, ok bool) {
	s := l10n.TL(lang, key, args)
	if s == "" || s == key {
		return "", false
	}
	return s, true
}

// unitResult 运行 fn，成功→Ok(Unit)，失败→按用户语言本地化错误 key 的 Err。
func (h *Hub) unitResult(user *User, fn func() error) protocol.StringResult[protocol.Unit] {
	if err := fn(); err != nil {
		return protocol.Errr[protocol.Unit](h.localize(user, err.Error()))
	}
	return protocol.Ok(protocol.Unit{})
}

// unitResultFromError 同 unitResult，但直接接收 error 而非闭包。
// 用于热路径（如 CmdPlayed）：避免闭包捕获 h/user/c 导致的堆分配
// （room-cycle 场景下闭包分配约 2.9 GB，占总分配 28%）。
func (h *Hub) unitResultFromError(user *User, err error) protocol.StringResult[protocol.Unit] {
	if err != nil {
		return protocol.Errr[protocol.Unit](h.localize(user, err.Error()))
	}
	return protocol.Ok(protocol.Unit{})
}

// errToStr 同 unitResult，但携带返回值 T。
func errToStr[T any](h *Hub, user *User, fn func() (T, error)) protocol.StringResult[T] {
	v, err := fn()
	if err != nil {
		return protocol.Errr[T](h.localize(user, err.Error()))
	}
	return protocol.Ok(v)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// ProcessClientCommand 处理一条已认证用户的客户端命令，返回需回复的命令（ok=false 表示无需回复）。
// 对应 TS network/session/commandRouter.processClientCommand。
func (h *Hub) ProcessClientCommand(user *User, cmd protocol.ClientCommand) (protocol.ServerCommand, bool) {
	switch c := cmd.(type) {
	case protocol.CmdPing:
		return nil, false

	case protocol.CmdAuthenticate:
		_ = c
		return protocol.SrvAuthenticate{Result: protocol.Errr[protocol.AuthInfo](h.localize(user, errAuthRepeated.Error()))}, true

	case protocol.CmdChat:
		return protocol.SrvChat{Result: h.unitResultFromError(user, h.handleChat(user, c))}, true

	case protocol.CmdTouches:
		return h.handleTouches(user, c)

	case protocol.CmdJudges:
		return h.handleJudges(user, c)

	case protocol.CmdCreateRoom:
		return protocol.SrvCreateRoom{Result: h.unitResultFromError(user, h.handleCreateRoom(user, c))}, true

	case protocol.CmdJoinRoom:
		return protocol.SrvJoinRoom{Result: h.handleJoinRoomResult(user, c)}, true

	case protocol.CmdLeaveRoom:
		return protocol.SrvLeaveRoom{Result: h.unitResultFromError(user, h.handleLeaveRoom(user))}, true

	case protocol.CmdLockRoom:
		return protocol.SrvLockRoom{Result: h.unitResultFromError(user, h.handleLockRoom(user, c))}, true

	case protocol.CmdCycleRoom:
		return protocol.SrvCycleRoom{Result: h.unitResultFromError(user, h.handleCycleRoom(user, c))}, true

	case protocol.CmdSelectChart:
		return protocol.SrvSelectChart{Result: h.unitResultFromError(user, h.handleSelectChart(user, c))}, true

	case protocol.CmdRequestStart:
		return protocol.SrvRequestStart{Result: h.unitResultFromError(user, h.handleRequestStart(user, c))}, true

	case protocol.CmdReady:
		return protocol.SrvReady{Result: h.unitResultFromError(user, h.handleReady(user))}, true

	case protocol.CmdCancelReady:
		return protocol.SrvCancelReady{Result: h.unitResultFromError(user, h.handleCancelReady(user))}, true

	case protocol.CmdPlayed:
		return protocol.SrvPlayed{Result: h.handlePlayedResult(user, c)}, true

	case protocol.CmdAbort:
		return protocol.SrvAbort{Result: h.unitResultFromError(user, h.handleAbort(user))}, true

	default:
		return nil, false
	}
}
