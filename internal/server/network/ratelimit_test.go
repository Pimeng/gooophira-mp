package network

import (
	"testing"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/common/platform/l10n"
	"github.com/Pimeng/gooophira-mp/internal/common/protocol"
)

func TestCategorize(t *testing.T) {
	cases := []struct {
		cmd  protocol.ClientCommand
		want rateCategory
	}{
		{protocol.CmdChat{}, catChat},
		{protocol.CmdSelectChart{}, catAPI},
		{protocol.CmdPlayed{}, catAPI},
		{protocol.CmdCreateRoom{}, catRoom},
		{protocol.CmdJoinRoom{}, catRoom},
		{protocol.CmdReady{}, catRoom},
		{protocol.CmdAbort{}, catRoom},
		{protocol.CmdPing{}, catNone},
		{protocol.CmdTouches{}, catNone},
		{protocol.CmdJudges{}, catNone},
		{protocol.CmdAuthenticate{}, catNone},
	}
	for _, c := range cases {
		if got := categorize(c.cmd); got != c.want {
			t.Errorf("categorize(%T) = %d, want %d", c.cmd, got, c.want)
		}
	}
}

// TestRateLimiter_OperationLimit 验证离散操作每秒最多 2 次（操作桶容量 2、补充 2/s）。
func TestRateLimiter_OperationLimit(t *testing.T) {
	now := time.Now()
	l := newCommandRateLimiter(now)

	// 前 2 个操作放行（操作桶容量 2，同时消耗 2 个总包令牌）。
	for i := range 2 {
		if !l.allow(catChat, now) {
			t.Fatalf("op %d should be allowed", i)
		}
	}
	// 第 3 个操作被操作桶拒绝（总包桶仍有余量）。
	if l.allow(catChat, now) {
		t.Error("3rd op should be denied (op bucket empty)")
	}
	// 1 秒后操作桶补充 2 个令牌 → 可再放行 2 个。
	later := now.Add(time.Second)
	for i := range 2 {
		if !l.allow(catRoom, later) {
			t.Fatalf("refilled op %d should be allowed", i)
		}
	}
	if l.allow(catAPI, later) {
		t.Error("3rd op after 1s refill should be denied")
	}
}

// TestRateLimiter_TotalPacketLimit 验证所有命令包每秒最多 15 个（含 catNone 实时数据）。
func TestRateLimiter_TotalPacketLimit(t *testing.T) {
	now := time.Now()
	l := newCommandRateLimiter(now)

	// catNone（Touches/Judges）不消耗操作令牌，但消耗总包令牌：前 15 个放行。
	for i := range 15 {
		if !l.allow(catNone, now) {
			t.Fatalf("catNone %d should be allowed within total budget", i)
		}
	}
	// 第 16 个被总包桶拒绝。
	if l.allow(catNone, now) {
		t.Error("16th catNone should be denied (total bucket empty)")
	}
	// 1 秒后总包桶补满 15 个。
	later := now.Add(time.Second)
	for i := range 15 {
		if !l.allow(catNone, later) {
			t.Fatalf("refilled catNone %d should be allowed", i)
		}
	}
}

// TestRateLimiter_TotalExhaustedBlocksOperations 验证总包桶耗尽时连操作也被拒。
func TestRateLimiter_TotalExhaustedBlocksOperations(t *testing.T) {
	now := time.Now()
	l := newCommandRateLimiter(now)

	// 先用 15 个 catNone 耗尽总包桶（操作桶仍满）。
	for range 15 {
		if !l.allow(catNone, now) {
			t.Fatal("catNone should be allowed within total budget")
		}
	}
	// 操作桶虽满，但总包桶空 → 操作也应被拒。
	if l.allow(catChat, now) {
		t.Error("op should be denied when total bucket exhausted")
	}
}

// TestRateLimiter_TotalCountsOperationsAndNone 验证操作与实时数据共享总包桶。
func TestRateLimiter_TotalCountsOperationsAndNone(t *testing.T) {
	now := time.Now()
	l := newCommandRateLimiter(now)

	// 2 个操作（消耗 2 操作 + 2 总包）。
	for i := range 2 {
		if !l.allow(catChat, now) {
			t.Fatalf("op %d should be allowed", i)
		}
	}
	// 操作桶空，但总包桶还剩 13 → catNone 仍可放行 13 个。
	for i := range 13 {
		if !l.allow(catNone, now) {
			t.Fatalf("catNone %d should be allowed (total remaining)", i)
		}
	}
	// 总包桶空 → catNone 被拒。
	if l.allow(catNone, now) {
		t.Error("catNone should be denied (total exhausted)")
	}
}

func TestRateLimitedResponse(t *testing.T) {
	lang := l10n.NewLanguage("en-US")
	// 请求-响应型命令 → 带错误结果的响应。
	resp, ok := rateLimitedResponse(lang, protocol.CmdChat{})
	if !ok {
		t.Fatal("chat should have a rate-limited response")
	}
	if c := resp.(protocol.SrvChat); c.Result.Ok || c.Result.Error == "" {
		t.Errorf("chat rate-limited result = %+v", c.Result)
	}
	// JoinRoom 的结果类型不同（JoinRoomResponse）。
	if jr, ok := rateLimitedResponse(lang, protocol.CmdJoinRoom{}); !ok || jr.(protocol.SrvJoinRoom).Result.Ok {
		t.Error("join room should have an error rate-limited response")
	}
	// 非请求-响应型 → 无响应。
	if _, ok := rateLimitedResponse(lang, protocol.CmdPing{}); ok {
		t.Error("ping should have no rate-limited response")
	}
}
