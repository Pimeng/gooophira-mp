// HTTP 适配器：经 HTTP POST 投递通用 JSON / Discord 等格式化载荷。
//
// 处理的 WebhookTarget.Type：generic / discord（未知类型也按 generic 处理）。
// feishu 类型不经本适配器（由 Feishu 适配器走 SDK），调用方负责按类型路由。
//
// 载荷经 webhook.Format 生成；可选 Secret 启用 HMAC-SHA256 签名头 X-Phira-Signature。
// 错误分类：URL 非法/4xx → 不可重试；网络/超时/429/5xx → 可重试。
package adapter

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/server"
)

// HTTP 是 HTTP POST 投递适配器。日志文案经 l10n 本地化（lang nil 走默认语言）。
type HTTP struct {
	client *http.Client
	logger Logger
	lang   *l10n.Language
}

// NewHTTP 创建 HTTP 适配器。httpClient 用于出站请求；logger 可为 nil（静默）；
// lang 决定日志文案语言，nil 走默认语言。
func NewHTTP(httpClient *http.Client, logger Logger, lang *l10n.Language) *HTTP {
	return &HTTP{client: httpClient, logger: logger, lang: lang}
}

// tl 把 l10n key + args 翻译为当前语言文本。
func (h *HTTP) tl(key string, args map[string]string) string {
	return l10n.TL(h.lang, key, args)
}

// debug 输出 Debug 级本地化日志。
func (h *HTTP) debug(key string, args map[string]string) {
	if h.logger != nil {
		h.logger.Debug(h.tl(key, args))
	}
}

// warn 输出 Warn 级本地化日志。
func (h *HTTP) warn(key string, args map[string]string) {
	if h.logger != nil {
		h.logger.Warn(h.tl(key, args))
	}
}

// Deliver 向单个 HTTP 目标投递一次格式化载荷。
// 返回 (成功, 失败时是否可重试)。载荷经 webhook.Format 生成；feishu 返回 nil 视为跳过（ok=true）。
func (h *HTTP) Deliver(ctx context.Context, t config.WebhookTarget, ev server.Event) (ok, retryable bool) {
	body, contentType := Format(t.Type, ev)
	if body == nil {
		// 该类型不走 HTTP（如 feishu）：静默跳过，不计失败、不重试。
		return true, false
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.URL, bytes.NewReader(body))
	if err != nil {
		// URL 非法等：不可重试
		return false, false
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", userAgent)
	if t.Secret != "" {
		mac := hmac.New(sha256.New, []byte(t.Secret))
		mac.Write(body)
		req.Header.Set("X-Phira-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}

	resp, err := h.client.Do(req)
	if err != nil {
		h.debug("log-webhook-request-error", map[string]string{"error": err.Error()})
		return false, true // 网络/超时：可重试
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 300 {
		return true, false
	}
	retryable = resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
	h.warn("log-webhook-bad-status", map[string]string{
		"url":       t.URL,
		"status":    strconv.Itoa(resp.StatusCode),
		"retryable": boolStr(retryable),
	})
	return false, retryable
}

// boolStr 把布尔值转为本地化"是/否"风格字符串（用于日志参数插值）。
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
