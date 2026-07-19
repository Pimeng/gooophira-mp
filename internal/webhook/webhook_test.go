package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/server"
	"github.com/Pimeng/gooophira-mp/internal/webhook/adapter"
	"github.com/Pimeng/gooophira-mp/internal/webhookmodel"
)

// capture 是一个收集收到的请求的测试服务器助手。
type capture struct {
	mu       sync.Mutex
	bodies   [][]byte
	headers  []http.Header
	hits     atomic.Int32
	status   int   // 返回状态码（0 → 200）
	failFirN int32 // 前 N 次返回 500，之后成功
}

func (c *capture) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n := c.hits.Add(1)
		body, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.bodies = append(c.bodies, body)
		c.headers = append(c.headers, r.Header.Clone())
		c.mu.Unlock()
		if n <= c.failFirN {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if c.status != 0 {
			w.WriteHeader(c.status)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func (c *capture) count() int { return int(c.hits.Load()) }

func (c *capture) lastBody() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.bodies) == 0 {
		return nil
	}
	return c.bodies[len(c.bodies)-1]
}

// waitFor 轮询直到 fn() 为真或超时。
func waitFor(t *testing.T, d time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", d)
}

func newDispatcher(t *testing.T, cfg *config.WebhookConfig) *Dispatcher {
	t.Helper()
	d := New(nil, nil)
	d.SetConfig(cfg)
	t.Cleanup(d.Close)
	return d
}

type targetCaptureAdapter struct {
	targets []config.WebhookTarget
}

func (a *targetCaptureAdapter) Deliver(_ context.Context, target config.WebhookTarget, _ webhookmodel.Event) (bool, bool) {
	a.targets = append(a.targets, target)
	return true, false
}

func TestDispatcherSplitsOneBotTargetIDArray(t *testing.T) {
	capture := &targetCaptureAdapter{}
	d := &Dispatcher{
		stop: make(chan struct{}),
		adapters: map[string]adapter.Adapter{
			"onebot_v11": capture,
		},
	}
	d.cfg.Store(&config.WebhookConfig{
		Enabled: true,
		Targets: []config.WebhookTarget{{
			Type: "onebot_v11", TargetID: 111, TargetIDs: []int64{111, 222, 333},
		}},
	})

	d.handle(webhookmodel.Event{Type: webhookmodel.EventGameEnd})

	if len(capture.targets) != 3 {
		t.Fatalf("delivery count=%d, want 3", len(capture.targets))
	}
	for i, want := range []int64{111, 222, 333} {
		if got := capture.targets[i]; got.TargetID != want || got.TargetIDs != nil {
			t.Fatalf("delivery %d target=%+v, want TargetID=%d and no TargetIDs", i, got, want)
		}
	}
}

func TestDispatcherDeliversGenericJSON(t *testing.T) {
	cap := &capture{}
	srv := httptest.NewServer(cap.handler())
	defer srv.Close()

	d := newDispatcher(t, &config.WebhookConfig{
		Enabled: true,
		Targets: []config.WebhookTarget{{URL: srv.URL, Type: "generic"}},
	})

	d.Emit(server.Event{Type: server.EventGameStart, RoomID: "ABCD", ChartName: "Test", UserCount: 3})
	waitFor(t, 2*time.Second, func() bool { return cap.count() == 1 })

	var got server.Event
	if err := json.Unmarshal(cap.lastBody(), &got); err != nil {
		t.Fatalf("body not valid event json: %v", err)
	}
	if got.Type != server.EventGameStart || got.RoomID != "ABCD" || got.UserCount != 3 {
		t.Fatalf("unexpected payload: %+v", got)
	}
}

func TestDispatcherEventFiltering(t *testing.T) {
	cap := &capture{}
	srv := httptest.NewServer(cap.handler())
	defer srv.Close()

	d := newDispatcher(t, &config.WebhookConfig{
		Enabled: true,
		Targets: []config.WebhookTarget{{URL: srv.URL, Events: []string{"game_end"}}},
	})

	// 未订阅：不应投递。
	d.Emit(server.Event{Type: server.EventGameStart, RoomID: "X"})
	// 已订阅：应投递。
	d.Emit(server.Event{Type: server.EventGameEnd, RoomID: "X"})

	waitFor(t, 2*time.Second, func() bool { return cap.count() == 1 })
	time.Sleep(50 * time.Millisecond) // 确认没有第二条漏网
	if cap.count() != 1 {
		t.Fatalf("expected exactly 1 delivery (game_end only), got %d", cap.count())
	}
}

func TestDispatcherRetriesOn5xx(t *testing.T) {
	cap := &capture{failFirN: 1} // 第一次 500，第二次成功
	srv := httptest.NewServer(cap.handler())
	defer srv.Close()

	d := New(nil, nil)
	d.SetConfig(&config.WebhookConfig{
		Enabled: true,
		Retries: 2,
		Targets: []config.WebhookTarget{{URL: srv.URL}},
	})
	t.Cleanup(d.Close)
	// 缩短重试退避以加速测试。
	d.Emit(server.Event{Type: server.EventGameEnd, RoomID: "R"})

	waitFor(t, 5*time.Second, func() bool { return cap.count() == 2 })
}

func TestDispatcherNoRetryOn4xx(t *testing.T) {
	cap := &capture{status: http.StatusBadRequest}
	srv := httptest.NewServer(cap.handler())
	defer srv.Close()

	d := newDispatcher(t, &config.WebhookConfig{
		Enabled: true,
		Retries: 3,
		Targets: []config.WebhookTarget{{URL: srv.URL}},
	})
	d.Emit(server.Event{Type: server.EventGameEnd, RoomID: "R"})

	waitFor(t, 2*time.Second, func() bool { return cap.count() == 1 })
	time.Sleep(50 * time.Millisecond)
	if cap.count() != 1 {
		t.Fatalf("4xx must not retry, got %d deliveries", cap.count())
	}
}

func TestDispatcherDisabledIsNoop(t *testing.T) {
	cap := &capture{}
	srv := httptest.NewServer(cap.handler())
	defer srv.Close()

	d := newDispatcher(t, &config.WebhookConfig{
		Enabled: false,
		Targets: []config.WebhookTarget{{URL: srv.URL}},
	})
	d.Emit(server.Event{Type: server.EventGameEnd, RoomID: "R"})

	time.Sleep(100 * time.Millisecond)
	if cap.count() != 0 {
		t.Fatalf("disabled dispatcher must not deliver, got %d", cap.count())
	}
}

func TestDispatcherHMACSignature(t *testing.T) {
	cap := &capture{}
	srv := httptest.NewServer(cap.handler())
	defer srv.Close()

	const secret = "s3cr3t"
	d := newDispatcher(t, &config.WebhookConfig{
		Enabled: true,
		Targets: []config.WebhookTarget{{URL: srv.URL, Secret: secret}},
	})
	d.Emit(server.Event{Type: server.EventGameEnd, RoomID: "R"})
	waitFor(t, 2*time.Second, func() bool { return cap.count() == 1 })

	cap.mu.Lock()
	body, hdr := cap.bodies[0], cap.headers[0]
	cap.mu.Unlock()

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if got := hdr.Get("X-Phira-Signature"); got != want {
		t.Fatalf("signature mismatch: got %q want %q", got, want)
	}
}

func TestFormatDiscordAndGeneric(t *testing.T) {
	ev := server.Event{Type: server.EventGameStart, Server: "S", RoomID: "RID", ChartName: "C", UserCount: 2}

	body, ct := Format("discord", ev)
	if ct == "" || body == nil {
		t.Fatal("discord format returned empty")
	}
	var dmsg struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(body, &dmsg); err != nil || dmsg.Content == "" {
		t.Fatalf("discord body invalid: %v body=%s", err, body)
	}

	// feishu 现走飞书开放平台 SDK（Dispatcher.deliverFeishu），Format 应返回 nil 跳过 HTTP 通道。
	body, _ = Format("feishu", ev)
	if body != nil {
		t.Fatalf("feishu should bypass HTTP channel, got body=%s", body)
	}

	// generic 仍输出完整结构化 JSON。
	body, _ = Format("generic", ev)
	var got server.Event
	if err := json.Unmarshal(body, &got); err != nil || got.RoomID != "RID" {
		t.Fatalf("generic body invalid: %v body=%s", err, body)
	}
}
