package network

import (
	"time"

	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

// 会话级命令限流（令牌桶）。已认证会话内的命令按类别限流，挡下刷屏聊天 / 放大上游 API
// 请求等异常洪水；实时数据（Touches/Judges）与心跳（Ping）完全不限。对应 TS
// network/session/commandRateLimiter.ts。

type rateCategory int

const (
	catNone rateCategory = iota
	catChat
	catAPI
	catRoom
	numCategories
)

// categorize 把命令映射到限流类别；catNone 表示不限流。
func categorize(cmd protocol.ClientCommand) rateCategory {
	switch cmd.(type) {
	case protocol.CmdChat:
		return catChat
	case protocol.CmdSelectChart, protocol.CmdPlayed:
		return catAPI
	case protocol.CmdCreateRoom, protocol.CmdJoinRoom, protocol.CmdLeaveRoom,
		protocol.CmdLockRoom, protocol.CmdCycleRoom, protocol.CmdRequestStart,
		protocol.CmdReady, protocol.CmdCancelReady, protocol.CmdAbort:
		return catRoom
	default:
		return catNone // Ping / Touches / Judges / Authenticate
	}
}

type bucketSpec struct {
	capacity     float64
	refillPerSec float64
}

// 默认参数：刻意宽松，正常游玩不会触发。
var defaultSpecs = [numCategories]bucketSpec{
	catChat: {capacity: 10, refillPerSec: 3},
	catAPI:  {capacity: 12, refillPerSec: 3},
	catRoom: {capacity: 20, refillPerSec: 6},
}

type bucket struct {
	tokens float64
	last   time.Time
}

// commandRateLimiter 每会话一个，持三类令牌桶。仅会话自身 goroutine 访问，无需加锁。
type commandRateLimiter struct {
	buckets [numCategories]bucket
}

func newCommandRateLimiter(now time.Time) *commandRateLimiter {
	l := &commandRateLimiter{}
	for cat := catChat; cat < numCategories; cat++ {
		l.buckets[cat] = bucket{tokens: defaultSpecs[cat].capacity, last: now}
	}
	return l
}

// allow 消费一个令牌；返回 false 表示超限应拒绝。catNone 始终放行。
func (l *commandRateLimiter) allow(cat rateCategory, now time.Time) bool {
	if cat == catNone {
		return true
	}
	spec := defaultSpecs[cat]
	b := &l.buckets[cat]
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = min(spec.capacity, b.tokens+elapsed*spec.refillPerSec)
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// rateLimitedResponse 为被限流的「请求-响应」型命令构造「操作过于频繁」错误响应，
// 避免客户端因收不到响应而挂起。非请求-响应型返回 (nil, false)。
func rateLimitedResponse(lang *l10n.Language, cmd protocol.ClientCommand) (protocol.ServerCommand, bool) {
	msg := l10n.TL(lang, "command-rate-limited", nil)
	unit := func() protocol.StringResult[protocol.Unit] { return protocol.Errr[protocol.Unit](msg) }
	switch cmd.(type) {
	case protocol.CmdChat:
		return protocol.SrvChat{Result: unit()}, true
	case protocol.CmdCreateRoom:
		return protocol.SrvCreateRoom{Result: unit()}, true
	case protocol.CmdJoinRoom:
		return protocol.SrvJoinRoom{Result: protocol.Errr[protocol.JoinRoomResponse](msg)}, true
	case protocol.CmdLeaveRoom:
		return protocol.SrvLeaveRoom{Result: unit()}, true
	case protocol.CmdLockRoom:
		return protocol.SrvLockRoom{Result: unit()}, true
	case protocol.CmdCycleRoom:
		return protocol.SrvCycleRoom{Result: unit()}, true
	case protocol.CmdSelectChart:
		return protocol.SrvSelectChart{Result: unit()}, true
	case protocol.CmdRequestStart:
		return protocol.SrvRequestStart{Result: unit()}, true
	case protocol.CmdReady:
		return protocol.SrvReady{Result: unit()}, true
	case protocol.CmdCancelReady:
		return protocol.SrvCancelReady{Result: unit()}, true
	case protocol.CmdPlayed:
		return protocol.SrvPlayed{Result: unit()}, true
	case protocol.CmdAbort:
		return protocol.SrvAbort{Result: unit()}, true
	default:
		return nil, false
	}
}
