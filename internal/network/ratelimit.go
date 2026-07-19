package network

import (
	"time"

	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

// 会话级命令限流（令牌桶）。每会话持两个桶：
//   - opBucket：所有「离散操作」（聊天 / 房间操作 / 上游 API 请求）合计 ≤ 2 次/秒。
//   - totalBucket：所有命令包（含实时 Touches/Judges）合计 ≤ 15 个/秒。
//
// 心跳（Ping）与认证（Authenticate）在 onCommand 限流前已提前返回，不计入任一桶。
// 实时数据（Touches/Judges）不消耗操作令牌，但消耗总包令牌，防止高频输入包洪水。
// 仅会话自身 readLoop goroutine 访问，无需加锁。

type rateCategory int

const (
	catNone rateCategory = iota
	catChat
	catAPI
	catRoom
)

// categorize 把命令映射到限流类别；catNone 表示非离散操作（Touches/Judges/Ping/Authenticate），
// 不消耗操作令牌。具体类别仍保留以便 rateLimitedResponse 构造响应与未来按类别精细化。
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
		return catNone // 对应 Ping、Touches、Judges、Authenticate。
	}
}

type bucketSpec struct {
	capacity     float64
	refillPerSec float64
}

// 限流参数用变量以便测试覆盖（与 phira.Client 重试参数同约定）。
var (
	// opSpec：离散操作每秒最多 2 次。
	opSpec = bucketSpec{capacity: 2, refillPerSec: 2}
	// totalSpec：所有命令包每秒最多 15 个。
	totalSpec = bucketSpec{capacity: 15, refillPerSec: 15}
)

type bucket struct {
	tokens float64
	last   time.Time
}

// commandRateLimiter 每会话一个，持操作桶与总包桶。
type commandRateLimiter struct {
	opBucket    bucket
	totalBucket bucket
}

func newCommandRateLimiter(now time.Time) *commandRateLimiter {
	return &commandRateLimiter{
		opBucket:    bucket{tokens: opSpec.capacity, last: now},
		totalBucket: bucket{tokens: totalSpec.capacity, last: now},
	}
}

// allow 消费令牌：总包桶始终消耗（每个收到的命令包都算）；离散操作额外消耗操作桶。
// 返回 false 表示超限应拒绝。总包桶耗尽时连 catNone（Touches/Judges）也会被拒。
func (l *commandRateLimiter) allow(cat rateCategory, now time.Time) bool {
	if !l.consume(&l.totalBucket, totalSpec, now) {
		return false
	}
	if cat == catNone {
		return true
	}
	return l.consume(&l.opBucket, opSpec, now)
}

// consume 按令牌桶算法消费一个令牌；先按时间间隔补充，不足则返回 false。
func (l *commandRateLimiter) consume(b *bucket, spec bucketSpec, now time.Time) bool {
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
