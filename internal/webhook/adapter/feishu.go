// Feishu 适配器：飞书开放平台 SDK，发交互式模板消息。
//
// 流程：若事件带 ImageURL，下载并经 im/v1/image.create 上传换取 image_key，
// 再填入模板变量 chart_pic，最后用 im/v1/message.create 以 interactive 模板发送。
//
// SDK 文档：https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/server-side-sdk/golang-sdk-guide/preparations
package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/server"
)

const (
	// feishuImageDownloadTimeout 限制下载谱面预览图的最长时间（防止外部图片服务卡死投递）。
	feishuImageDownloadTimeout = 30 * time.Second
	// feishuImageMaxBytes 飞书上传图片大小上限 10MB。
	feishuImageMaxBytes = 10 * 1024 * 1024
)

// feishuClientKey 以 (AppID,AppSecret) 为缓存键的连接共享粒度。
type feishuClientKey struct {
	appID     string
	appSecret string
}

// Feishu 是飞书开放平台 SDK 适配器。lark.Client 按 (AppID,AppSecret) 缓存复用
// （SDK 自带 token 管理与刷新）。多次热重载配置后同凭据目标共享同一实例。
//
// 日志文案经 l10n.TL(lang, key, args) 本地化后再写入 logger，与 main.go 既有
// 日志输出风格一致；lang 为 nil 时按默认语言（zh-CN）输出。
type Feishu struct {
	httpClient *http.Client
	logger     Logger
	lang       *l10n.Language

	mu      sync.RWMutex
	clients map[feishuClientKey]*lark.Client
}

// NewFeishu 创建飞书适配器。httpClient 用于下载事件 ImageURL 指向的图片；
// logger 可为 nil（静默）；lang 决定日志文案语言，nil 走默认语言。
func NewFeishu(httpClient *http.Client, logger Logger, lang *l10n.Language) *Feishu {
	return &Feishu{
		httpClient: httpClient,
		logger:     logger,
		lang:       lang,
		clients:    make(map[feishuClientKey]*lark.Client),
	}
}

// tl 把 l10n key + args 翻译为当前语言文本；logger 为 nil 时返回空串。
func (f *Feishu) tl(key string, args map[string]string) string {
	return l10n.TL(f.lang, key, args)
}

// debug 输出 Debug 级本地化日志。
func (f *Feishu) debug(key string, args map[string]string) {
	if f.logger != nil {
		f.logger.Debug(f.tl(key, args))
	}
}

// warn 输出 Warn 级本地化日志。
func (f *Feishu) warn(key string, args map[string]string) {
	if f.logger != nil {
		f.logger.Warn(f.tl(key, args))
	}
}

// Deliver 向单个飞书 SDK 目标投递一条事件模板消息（单次请求）。
// 返回 (成功, 失败时是否可重试)。重试/超时由调用方（webhook.Dispatcher）控制。
func (f *Feishu) Deliver(ctx context.Context, t config.WebhookTarget, ev server.Event) (ok, retryable bool) {
	client := f.larkClient(t.AppID, t.AppSecret)

	tplVars := feishuTemplateVariables(ev)

	// 若事件带 ImageURL，下载并上传至飞书换取 image_key，填入模板变量 chart_pic。
	if ev.ImageURL != "" {
		imgKey, err := f.uploadImageFromURL(ctx, client, ev.ImageURL)
		if err != nil {
			f.debug("log-feishu-upload-failed", map[string]string{"error": err.Error()})
			return false, true // 图片下载或上传属网络/外部错误：可重试
		}
		tplVars["chart_pic"] = map[string]any{"img_key": imgKey}
	} else {
		// 无图时仍占位，避免模板渲染缺变量报错。
		tplVars["chart_pic"] = map[string]any{"img_key": ""}
	}

	content := feishuTemplateContent(t, tplVars)
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType("open_id").
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(t.ReceiveOpenID).
			MsgType("interactive").
			Content(content).
			Build()).
		Build()

	resp, err := client.Im.V1.Message.Create(ctx, req)
	if err != nil {
		f.debug("log-feishu-send-error", map[string]string{"error": err.Error()})
		return false, true // 传输/超时：可重试
	}
	if !resp.Success() {
		// 服务端业务错误：通常为权限/配额/模板配置问题，不重试。
		f.warn("log-feishu-send-failed", map[string]string{
			"logId": resp.RequestId(),
			"code":  strconv.Itoa(resp.Code),
			"msg":   resp.Msg,
		})
		return false, false
	}
	return true, false
}

// larkClient 返回（或创建并缓存）指定应用凭据的 lark 客户端。
func (f *Feishu) larkClient(appID, appSecret string) *lark.Client {
	k := feishuClientKey{appID: appID, appSecret: appSecret}
	f.mu.RLock()
	if c, ok := f.clients[k]; ok {
		f.mu.RUnlock()
		return c
	}
	f.mu.RUnlock()

	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.clients[k]; ok {
		return c
	}
	c := lark.NewClient(appID, appSecret)
	f.clients[k] = c
	return c
}

// uploadImageFromURL 下载指定 URL 的图片，上传到飞书开放平台（image_type=message）
// 返回可填入模板变量的 image_key。
func (f *Feishu) uploadImageFromURL(ctx context.Context, client *lark.Client, imageURL string) (string, error) {
	dlCtx, dlCancel := context.WithTimeout(ctx, feishuImageDownloadTimeout)
	defer dlCancel()
	dlReq, err := http.NewRequestWithContext(dlCtx, http.MethodGet, imageURL, nil)
	if err != nil {
		return "", fmt.Errorf("build image request: %w", err)
	}
	dlReq.Header.Set("User-Agent", userAgent)
	dlResp, err := f.httpClient.Do(dlReq)
	if err != nil {
		return "", fmt.Errorf("download image: %w", err)
	}
	defer dlResp.Body.Close()
	if dlResp.StatusCode >= 400 {
		return "", fmt.Errorf("download image status %d", dlResp.StatusCode)
	}
	imgBytes, err := io.ReadAll(io.LimitReader(dlResp.Body, feishuImageMaxBytes))
	if err != nil {
		return "", fmt.Errorf("read image body: %w", err)
	}
	if len(imgBytes) == 0 {
		return "", fmt.Errorf("empty image body")
	}

	upReq := larkim.NewCreateImageReqBuilder().
		Body(larkim.NewCreateImageReqBodyBuilder().
			ImageType("message").
			Image(bytes.NewReader(imgBytes)).
			Build()).
		Build()
	upResp, err := client.Im.V1.Image.Create(ctx, upReq)
	if err != nil {
		return "", fmt.Errorf("upload image: %w", err)
	}
	if !upResp.Success() {
		return "", fmt.Errorf("upload image code=%d msg=%s", upResp.Code, upResp.Msg)
	}
	if upResp.Data == nil || upResp.Data.ImageKey == nil {
		return "", fmt.Errorf("upload image: no image_key in response")
	}
	return *upResp.Data.ImageKey, nil
}

// feishuTemplateVariables 把事件字段映射到飞书模板变量。
// chart_pic 由调用方在图片上传成功后覆写为 {"img_key": ...}。
func feishuTemplateVariables(ev server.Event) map[string]any {
	return map[string]any{
		"room_id":          ev.RoomID,
		"chart_name":       ev.ChartName,
		"player_list":      ev.PlayerList,
		"chart_difficulty": ev.ChartDifficulty,
		"chart_charter":    ev.ChartCharter,
	}
}

// feishuTemplateContent 组装消息 content 字段（飞书交互式模板信封）。
func feishuTemplateContent(t config.WebhookTarget, vars map[string]any) string {
	envelope := map[string]any{
		"type": "template",
		"data": map[string]any{
			"template_id":           t.TemplateID,
			"template_version_name": t.TemplateVersion,
			"template_variable":     vars,
		},
	}
	b, _ := json.Marshal(envelope)
	return string(b)
}
