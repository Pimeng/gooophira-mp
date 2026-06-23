package network

import (
	"testing"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
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

func TestRateLimiter_BucketAndRefill(t *testing.T) {
	now := time.Now()
	l := newCommandRateLimiter(now)

	// chat 容量 10：前 10 放行，第 11 拒绝。
	for i := range 10 {
		if !l.allow(catChat, now) {
			t.Fatalf("chat %d should be allowed", i)
		}
	}
	if l.allow(catChat, now) {
		t.Error("11th chat should be denied (bucket empty)")
	}
	// 1 秒后补充 3 个令牌（refill 3/s）→ 可再放行 3 个。
	later := now.Add(time.Second)
	for i := range 3 {
		if !l.allow(catChat, later) {
			t.Fatalf("refilled chat %d should be allowed", i)
		}
	}
	if l.allow(catChat, later) {
		t.Error("4th after 1s refill should be denied")
	}
}

func TestRateLimiter_NoneNeverLimited(t *testing.T) {
	now := time.Now()
	l := newCommandRateLimiter(now)
	for range 1000 {
		if !l.allow(catNone, now) {
			t.Fatal("catNone should never be limited")
		}
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
