// Package webhook 把服务器事件（对局开始/结束、建房/解散、加入、维护切换）异步外发到
// 可配置的目标（通用 JSON / Discord / OneBot v11 经 HTTP；飞书走开放平台 SDK 等）。
//
// 设计要点：
//   - Dispatcher 实现 server.EventSink，Emit 仅向带缓冲的 channel 入队（非阻塞，满即丢弃），
//     绝不阻塞命令处理（Emit 可能在持有 ServerState.Mu 时被调用）。
//   - 单后台 worker 串行消费事件，按各目标的订阅过滤后逐个投递；每次请求带超时与有限重试，
//     失败仅记日志、不影响服务。事件量低（开局/结束等），串行足够且易测。
//   - 配置经 atomic.Pointer 热替换：WEBHOOK 配置热重载后调用 SetConfig 即时生效。
//   - 投递通道抽象为 adapter.Adapter 接口（internal/webhook/adapter）：
//     Dispatcher 仅做超时/重试/停止编排与按 Type 路由，平台细节由适配器实现。
//     新增平台只需在 adapter 包实现 Adapter 并在 New 时按 Type 注册。
package webhook

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/netutil"
	"github.com/Pimeng/gooophira-mp/internal/server"
	"github.com/Pimeng/gooophira-mp/internal/webhook/adapter"
)

// Logger 是本包所需的最小日志接口（与 server.Logger 兼容，便于注入）。
type Logger interface {
	Debug(msg string)
	Warn(msg string)
}

const (
	queueSize  = 256             // 事件缓冲队列容量（满即丢弃，保护命令处理）
	retryDelay = 2 * time.Second // 重试基础退避
)

// Dispatcher 是 Webhook 投递器，实现 server.EventSink。
type Dispatcher struct {
	logger Logger
	lang   *l10n.Language
	cfg    atomic.Pointer[config.WebhookConfig]

	ch   chan server.Event
	stop chan struct{}
	wg   sync.WaitGroup

	// adapters 按 WebhookTarget.Type 路由到对应适配器。
	adapters map[string]adapter.Adapter
}

// 编译期断言：Dispatcher 满足 server.EventSink。
var _ server.EventSink = (*Dispatcher)(nil)

// New 创建并启动投递器。logger 可为 nil（静默）；lang 决定飞书适配器日志文案语言，
// nil 走 l10n 默认语言。
// 出站 HTTP 客户端经 netutil.NewClient() 构造（Android 注入公共 DNS 解析，
// 其它平台走系统 resolver）。默认注册 generic/discord→HTTP、onebot_v11→OneBotV11、
// feishu→Feishu 适配器。
func New(logger Logger, lang *l10n.Language) *Dispatcher {
	httpClient := netutil.NewClient() // 单次请求超时经 context 控制（按目标配置）
	d := &Dispatcher{
		logger:   logger,
		lang:     lang,
		ch:       make(chan server.Event, queueSize),
		stop:     make(chan struct{}),
		adapters: make(map[string]adapter.Adapter),
	}
	// HTTP 适配器处理 generic / discord（未注册的 Type 也回退到它）。
	d.adapters["generic"] = adapter.NewHTTP(httpClient, logger, lang)
	d.adapters["discord"] = adapter.NewHTTP(httpClient, logger, lang)
	d.adapters["onebot_v11"] = adapter.NewOneBotV11(httpClient)
	d.adapters["feishu"] = adapter.NewFeishu(httpClient, logger, lang)
	d.wg.Add(1)
	go d.run()
	return d
}

// SetConfig 热替换 Webhook 配置（启动期与每次热重载调用）。nil = 关闭。
func (d *Dispatcher) SetConfig(c *config.WebhookConfig) {
	d.cfg.Store(c)
	if c == nil {
		return
	}
	if f, ok := d.adapters["feishu"].(*adapter.Feishu); ok {
		f.Prewarm(c.Targets)
	}
}

// Emit 入队一个事件（非阻塞）。未启用或队列满时静默丢弃。
func (d *Dispatcher) Emit(ev server.Event) {
	c := d.cfg.Load()
	if c == nil || !c.Enabled || len(c.Targets) == 0 {
		return
	}
	select {
	case d.ch <- ev:
	default:
		d.debug("log-webhook-queue-dropped", map[string]string{"event": string(ev.Type)})
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
		a, ok := d.adapters[t.Type]
		if !ok {
			// 未知 Type 回退到 generic（HTTP 适配器）。
			a, ok = d.adapters["generic"]
			if !ok {
				continue
			}
		}
		if t.Type == "onebot_v11" && len(t.TargetIDs) > 1 {
			// 每个目标独立重试，避免部分成功后重试整组造成重复消息。
			for _, targetID := range t.TargetIDs {
				singleTarget := t
				singleTarget.TargetID = targetID
				singleTarget.TargetIDs = nil
				d.deliver(a, singleTarget, ev, timeout, retries)
			}
			continue
		}
		d.deliver(a, t, ev, timeout, retries)
	}
}

// deliver 向单个目标投递，由对应 Adapter 执行单次平台调用；失败按退避重试。
// 适配器返回可重试则重试；业务错误不重试；跳过（ok=true）视为成功。
func (d *Dispatcher) deliver(a adapter.Adapter, t config.WebhookTarget, ev server.Event, timeout time.Duration, retries int) {
	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 {
			select {
			case <-d.stop:
				return
			case <-time.After(retryDelay * time.Duration(attempt)):
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		ok, retryable := a.Deliver(ctx, t, ev)
		cancel()
		if ok {
			return
		}
		if !retryable {
			d.warn("log-webhook-rejected", map[string]string{
				"event": string(ev.Type),
				"type":  t.Type,
				"url":   t.URL,
			})
			return
		}
	}
	d.warn("log-webhook-gave-up", map[string]string{
		"event": string(ev.Type),
		"type":  t.Type,
		"url":   t.URL,
	})
}

// tl 把 l10n key + args 翻译为当前语言文本。
func (d *Dispatcher) tl(key string, args map[string]string) string {
	return l10n.TL(d.lang, key, args)
}

// debug 输出 Debug 级本地化日志。
func (d *Dispatcher) debug(key string, args map[string]string) {
	if d.logger != nil {
		d.logger.Debug(d.tl(key, args))
	}
}

// warn 输出 Warn 级本地化日志。
func (d *Dispatcher) warn(key string, args map[string]string) {
	if d.logger != nil {
		d.logger.Warn(d.tl(key, args))
	}
}
