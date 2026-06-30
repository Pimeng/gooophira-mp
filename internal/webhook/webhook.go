// Package webhook 把服务器事件（对局开始/结束、建房/解散、加入、维护切换）异步外发到
// 可配置的 HTTP 端点（通用 JSON / Discord / 飞书 等群机器人）。
//
// 设计要点：
//   - Dispatcher 实现 server.EventSink，Emit 仅向带缓冲的 channel 入队（非阻塞，满即丢弃），
//     绝不阻塞命令处理（Emit 可能在持有 ServerState.Mu 时被调用）。
//   - 单后台 worker 串行消费事件，按各目标的订阅过滤后逐个投递；每次请求带超时与有限重试，
//     失败仅记日志、不影响服务。事件量低（开局/结束等），串行足够且易测。
//   - 配置经 atomic.Pointer 热替换：WEBHOOK 配置热重载后调用 SetConfig 即时生效。
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/server"
)

// Logger 是本包所需的最小日志接口（与 server.Logger 兼容，便于注入）。
type Logger interface {
	Debug(msg string)
	Warn(msg string)
}

const (
	queueSize  = 256             // 事件缓冲队列容量（满即丢弃，保护命令处理）
	retryDelay = 2 * time.Second // 重试基础退避
	userAgent  = "phira-mp-webhook"
)

// Dispatcher 是 Webhook 投递器，实现 server.EventSink。
type Dispatcher struct {
	logger Logger
	client *http.Client
	cfg    atomic.Pointer[config.WebhookConfig]

	ch   chan server.Event
	stop chan struct{}
	wg   sync.WaitGroup
}

// 编译期断言：Dispatcher 满足 server.EventSink。
var _ server.EventSink = (*Dispatcher)(nil)

// New 创建并启动投递器。logger 可为 nil（静默）。
func New(logger Logger) *Dispatcher {
	d := &Dispatcher{
		logger: logger,
		client: &http.Client{}, // 单次请求超时经 context 控制（按目标配置）
		ch:     make(chan server.Event, queueSize),
		stop:   make(chan struct{}),
	}
	d.wg.Add(1)
	go d.run()
	return d
}

// SetConfig 热替换 Webhook 配置（启动期与每次热重载调用）。nil = 关闭。
func (d *Dispatcher) SetConfig(c *config.WebhookConfig) { d.cfg.Store(c) }

// Emit 入队一个事件（非阻塞）。未启用或队列满时静默丢弃。
func (d *Dispatcher) Emit(ev server.Event) {
	c := d.cfg.Load()
	if c == nil || !c.Enabled || len(c.Targets) == 0 {
		return
	}
	select {
	case d.ch <- ev:
	default:
		d.debugf("webhook: queue full, dropping event " + string(ev.Type))
	}
}

// Close 停止 worker 并等待退出（幂等地由调用方保证只调一次）。
func (d *Dispatcher) Close() {
	close(d.stop)
	d.wg.Wait()
}

func (d *Dispatcher) run() {
	defer d.wg.Done()
	for {
		select {
		case <-d.stop:
			return
		case ev := <-d.ch:
			d.handle(ev)
		}
	}
}

func (d *Dispatcher) handle(ev server.Event) {
	c := d.cfg.Load()
	if c == nil || !c.Enabled {
		return
	}
	timeout := time.Duration(c.WebhookTimeoutMS()) * time.Millisecond
	retries := c.WebhookRetryCount()
	for _, t := range c.Targets {
		if !t.Subscribes(string(ev.Type)) {
			continue
		}
		body, contentType := Format(t.Type, ev)
		if body == nil {
			continue
		}
		d.post(t, body, contentType, timeout, retries, string(ev.Type))
	}
}

// post 向单个目标投递，失败按退避重试。仅 5xx/429/网络错误重试；其它 4xx 视为客户端配置问题不重试。
func (d *Dispatcher) post(t config.WebhookTarget, body []byte, contentType string, timeout time.Duration, retries int, evType string) {
	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 {
			select {
			case <-d.stop:
				return
			case <-time.After(retryDelay * time.Duration(attempt)):
			}
		}
		ok, retryable := d.doRequest(t, body, contentType, timeout)
		if ok {
			return
		}
		if !retryable {
			d.warnf("webhook: target rejected event " + evType + " (" + t.URL + "), not retrying")
			return
		}
	}
	d.warnf("webhook: gave up delivering event " + evType + " to " + t.URL)
}

// doRequest 发一次请求；返回 (成功, 失败时是否可重试)。
func (d *Dispatcher) doRequest(t config.WebhookTarget, body []byte, contentType string, timeout time.Duration) (ok, retryable bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.URL, bytes.NewReader(body))
	if err != nil {
		return false, false // URL 非法等：不可重试
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", userAgent)
	if t.Secret != "" {
		mac := hmac.New(sha256.New, []byte(t.Secret))
		mac.Write(body)
		req.Header.Set("X-Phira-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}

	resp, err := d.client.Do(req)
	if err != nil {
		d.debugf("webhook: request error: " + err.Error())
		return false, true // 网络/超时：可重试
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 300 {
		return true, false
	}
	retryable = resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
	return false, retryable
}

func (d *Dispatcher) debugf(msg string) {
	if d.logger != nil {
		d.logger.Debug(msg)
	}
}

func (d *Dispatcher) warnf(msg string) {
	if d.logger != nil {
		d.logger.Warn(msg)
	}
}
