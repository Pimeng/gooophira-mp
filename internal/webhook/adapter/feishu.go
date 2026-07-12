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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/disintegration/imaging"
	"github.com/google/uuid"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/server"
)

const (
	// feishuImageDownloadTimeout 限制下载谱面预览图的最长时间（防止外部图片服务卡死投递）。
	feishuImageDownloadTimeout = 30 * time.Second
	// feishuImageHardLimit 下载阶段硬上限（30 MB）：原图超过此大小直接放弃，不下载完整文件。
	// Phira 封面图偶有 10 MB+ 的高分辨率 PNG，给一定冗余以便压缩处理。
	feishuImageHardLimit = 30 * 1000 * 1000
	// feishuImageMaxBytes 飞书上传图片大小上限（软限）10 MB。超过则本地压缩到 ≤10 MB 再上传。
	// 飞书侧按十进制 10,000,000 字节计，故此处亦用十进制。
	feishuImageMaxBytes = 10 * 1000 * 1000

	// 内置飞书模板 ID 与版本号（不走配置，直接硬编码）。
	// feishuGameStartTemplateID 用于 game_start 事件，模板变量含谱面信息与预览图。
	feishuGameStartTemplateID      = "AAqW6KwgHwCLU"
	feishuGameStartTemplateVersion = "1.0.2"
	// feishuGameEndTemplateID 用于 game_end 事件，模板变量含房间成绩排行。
	feishuGameEndTemplateID      = "AAqWqHbH8h8Vp"
	feishuGameEndTemplateVersion = "1.0.1"
)

// feishuClientKey 以 (AppID,AppSecret) 为缓存键的连接共享粒度。
type feishuClientKey struct {
	appID     string
	appSecret string
}

// liveUpdateEntry 跟踪单个房间+接收人的流式更新卡片状态。
type liveUpdateEntry struct {
	messageID string // 发送成功后的消息 ID（空表示尚未发送成功）
	sendUUID  string // 首次发送的幂等 uuid，用于 sendMessage 去重
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

	// 图片去重缓存：相同字节内容（sha256）映射到已上传的 image_key，
	// 避免同一谱面预览图在多目标/多事件重复上传飞书（飞书 image_key 在同租户内可复用）。
	imgMu    sync.Mutex
	imgCache map[string]string // sha256(hex) → image_key

	// 流式更新状态：key 为 roomID+"|"+receiveOpenID，value 为卡片实体状态。
	liveMu sync.Mutex
	msgIDs map[string]*liveUpdateEntry
}

// NewFeishu 创建飞书适配器。httpClient 用于下载事件 ImageURL 指向的图片；
// logger 可为 nil（静默）；lang 决定日志文案语言，nil 走默认语言。
func NewFeishu(httpClient *http.Client, logger Logger, lang *l10n.Language) *Feishu {
	return &Feishu{
		httpClient: httpClient,
		logger:     logger,
		lang:       lang,
		clients:    make(map[feishuClientKey]*lark.Client),
		imgCache:   make(map[string]string),
		msgIDs:     make(map[string]*liveUpdateEntry),
	}
}

// tl 把 l10n key + args 翻译为当前语言文本；logger 为 nil 时返回空串。
func (f *Feishu) tl(key string, args map[string]string) string {
	return l10n.TL(f.lang, key, args)
}

func (f *Feishu) debug(message string) {
	if f.logger != nil {
		f.logger.Debug(message)
	}
}

// warn 输出 Warn 级本地化日志。
func (f *Feishu) warn(key string, args map[string]string) {
	if f.logger != nil {
		f.logger.Warn(f.tl(key, args))
	}
}

// Deliver 向单个飞书 SDK 目标投递一条事件（单次请求）。
// EventGameStart 走交互式模板（含图片上传换 image_key，展示谱面预览图等富信息）；
// EventGameEnd 走卡片模板（含成绩排行），LiveUpdate 开启时优先 PATCH 已有卡片；
// EventScoreSubmitted 走流式更新（首次发送卡片，后续 PATCH）；
// EventRoomDisband 清理流式更新状态；
// 其余事件走纯文本（msg_type=text），内容用 RenderText(ev) 生成，降低模板配额占用。
// 返回 (成功, 失败时是否可重试)。重试/超时由调用方（webhook.Dispatcher）控制。
func (f *Feishu) Deliver(ctx context.Context, t config.WebhookTarget, ev server.Event) (ok, retryable bool) {
	client := f.larkClient(t.AppID, t.AppSecret)
	f.debug(fmt.Sprintf("开始投递飞书事件：%s", ev.Type))
	switch ev.Type {
	case server.EventGameStart:
		return f.deliverTemplate(ctx, client, t, ev)
	case server.EventScoreSubmitted:
		if !t.LiveUpdate {
			return true, false // 未启用流式更新：跳过
		}
		if len(ev.PlayerScoreRank) == 0 {
			return true, false // 无成绩：跳过
		}
		return f.deliverScoreUpdate(ctx, client, t, ev)
	case server.EventGameEnd:
		if t.LiveUpdate {
			if ok, retryable := f.deliverGameEndLive(ctx, client, t, ev); ok || !retryable {
				return ok, retryable
			}
			// entry 无 messageID：降级为一次性发送
		}
		// 无成绩时飞书模板的表格行校验失败（code=230099, table rows is invalid），
		// 降级为纯文本投递，避免模板渲染错误导致事件丢失。
		if len(ev.PlayerScoreRank) == 0 {
			return f.deliverText(ctx, client, t, ev)
		}
		_, ok, retryable = f.deliverGameEndTemplate(ctx, client, t, ev, "")
		return ok, retryable
	case server.EventRoomDisband:
		f.cleanLiveUpdateEntries(ev.RoomID)
		return true, false
	default:
		return f.deliverText(ctx, client, t, ev)
	}
}

// liveUpdateKey 构造流式更新状态的 map key。
func liveUpdateKey(roomID, receiveOpenID string) string {
	return roomID + "|" + receiveOpenID
}

// deliverScoreUpdate 处理 ScoreSubmitted 事件：首次发送卡片，后续 PATCH 更新。
func (f *Feishu) deliverScoreUpdate(ctx context.Context, client *lark.Client, t config.WebhookTarget, ev server.Event) (ok, retryable bool) {
	key := liveUpdateKey(ev.RoomID, t.ReceiveOpenID)
	f.liveMu.Lock()
	entry := f.msgIDs[key]
	if entry == nil {
		entry = &liveUpdateEntry{sendUUID: uuid.New().String()}
		f.msgIDs[key] = entry
	}
	f.liveMu.Unlock()
	if entry.messageID == "" {
		// 首次发送（或上次发送超时未拿到 messageID，用相同 uuid 重试去重）
		id, ok, retryable := f.deliverGameEndTemplate(ctx, client, t, ev, entry.sendUUID)
		if ok && id != "" {
			f.liveMu.Lock()
			entry.messageID = id
			f.liveMu.Unlock()
		}
		return ok, retryable
	}
	return f.patchGameEndTemplate(ctx, client, t, ev, entry.messageID)
}

// deliverGameEndLive 处理 LiveUpdate 开启时的 GameEnd 事件。
// 已有卡片则最终 PATCH 并清理；无卡片返回 (false, true) 让调用方降级。
func (f *Feishu) deliverGameEndLive(ctx context.Context, client *lark.Client, t config.WebhookTarget, ev server.Event) (ok, retryable bool) {
	key := liveUpdateKey(ev.RoomID, t.ReceiveOpenID)
	f.liveMu.Lock()
	entry := f.msgIDs[key]
	f.liveMu.Unlock()
	if entry == nil || entry.messageID == "" {
		return false, true // 无卡片：降级为一次性发送
	}
	defer func() {
		f.liveMu.Lock()
		delete(f.msgIDs, key)
		f.liveMu.Unlock()
	}()
	if len(ev.PlayerScoreRank) == 0 {
		// 有卡片但无成绩：用文本更新（不可能正常发生，兜底）
		return f.deliverText(ctx, client, t, ev)
	}
	return f.patchGameEndTemplate(ctx, client, t, ev, entry.messageID)
}

// cleanLiveUpdateEntries 清理指定房间的所有流式更新状态（房间解散时调用）。
func (f *Feishu) cleanLiveUpdateEntries(roomID string) {
	prefix := roomID + "|"
	f.liveMu.Lock()
	for k := range f.msgIDs {
		if strings.HasPrefix(k, prefix) {
			delete(f.msgIDs, k)
		}
	}
	f.liveMu.Unlock()
}

// deliverTemplate 投递 EventGameStart 的交互式模板消息。
func (f *Feishu) deliverTemplate(ctx context.Context, client *lark.Client, t config.WebhookTarget, ev server.Event) (ok, retryable bool) {
	tplVars := feishuTemplateVariables(ev)
	chartPicKey := ""

	// 若事件带 ImageURL，下载并上传至飞书换取 image_key，填入模板变量 chart_pic。
	if ev.ImageURL != "" {
		imgKey, err := f.uploadImageFromURL(ctx, client, ev.ImageURL)
		if err != nil {
			f.debug(fmt.Sprintf("飞书图片上传失败：%v", err))
			return false, true // 图片下载或上传属网络/外部错误：可重试
		}
		tplVars["chart_pic"] = map[string]any{"img_key": imgKey}
		chartPicKey = imgKey
		f.debug(fmt.Sprintf("飞书模板图片变量 chart_pic 已就绪，img_key=%s", imgKey))
	} else {
		// 无图时仍占位，避免模板渲染缺变量报错。
		tplVars["chart_pic"] = map[string]any{"img_key": ""}
	}
	// 模板 ID：配置覆盖优先，留空走内置默认。
	gameStartTplID := t.TemplateID
	if gameStartTplID == "" {
		gameStartTplID = feishuGameStartTemplateID
	}
	f.debug(fmt.Sprintf("飞书模板投递内容：template_id=%s，version=%s，room_id=%s，chart_name=%s，difficulty=%s，charter=%s，player_list=%s，chart_pic.img_key=%s",
		gameStartTplID, feishuGameStartTemplateVersion, ev.RoomID, ev.ChartName, ev.ChartDifficulty, ev.ChartCharter, ev.PlayerList, chartPicKey))

	_, ok, retryable = f.sendMessage(ctx, client, t, "interactive", feishuTemplateContentRaw(gameStartTplID, feishuGameStartTemplateVersion, tplVars), "")
	return ok, retryable
}

// deliverGameEndTemplate 投递 EventGameEnd 的卡片模板消息（含成绩排行）。
// reqUUID 非空时设入请求实现幂等去重；返回 messageID 供流式更新 PATCH 使用。
func (f *Feishu) deliverGameEndTemplate(ctx context.Context, client *lark.Client, t config.WebhookTarget, ev server.Event, reqUUID string) (messageID string, ok, retryable bool) {
	// player_score_rank 作为数组对象传入模板变量，供模板表格组件迭代生成行。
	// 不可传 JSON 字符串：飞书表格组件无法迭代字符串，会报 table rows is invalid（230099）。
	tplVars := map[string]any{
		"room_id":           ev.RoomID,
		"player_score_rank": ev.PlayerScoreRank,
	}
	// 模板 ID：配置覆盖优先，留空走内置默认。
	gameEndTplID := t.GameEndTemplateID
	if gameEndTplID == "" {
		gameEndTplID = feishuGameEndTemplateID
	}
	f.debug(fmt.Sprintf("飞书 game_end 模板投递：template_id=%s，version=%s，room_id=%s，rank_len=%d",
		gameEndTplID, feishuGameEndTemplateVersion, ev.RoomID, len(ev.PlayerScoreRank)))
	return f.sendMessage(ctx, client, t, "interactive", feishuTemplateContentRaw(gameEndTplID, feishuGameEndTemplateVersion, tplVars), reqUUID)
}

// patchGameEndTemplate 用 PATCH 更新已发送的 game_end 卡片（成绩实时刷新）。
func (f *Feishu) patchGameEndTemplate(ctx context.Context, client *lark.Client, t config.WebhookTarget, ev server.Event, messageID string) (ok, retryable bool) {
	tplVars := map[string]any{
		"room_id":           ev.RoomID,
		"player_score_rank": ev.PlayerScoreRank,
	}
	gameEndTplID := t.GameEndTemplateID
	if gameEndTplID == "" {
		gameEndTplID = feishuGameEndTemplateID
	}
	f.debug(fmt.Sprintf("飞书 game_end 模板 PATCH：template_id=%s，version=%s，room_id=%s，rank_len=%d，messageID=%s",
		gameEndTplID, feishuGameEndTemplateVersion, ev.RoomID, len(ev.PlayerScoreRank), messageID))
	return f.patchMessage(ctx, client, messageID, feishuTemplateContentRaw(gameEndTplID, feishuGameEndTemplateVersion, tplVars))
}

// deliverText 投递纯文本消息（msg_type=text）。内容用 RenderText 生成。
func (f *Feishu) deliverText(ctx context.Context, client *lark.Client, t config.WebhookTarget, ev server.Event) (ok, retryable bool) {
	text := RenderText(ev)
	f.debug(fmt.Sprintf("飞书文本投递内容：%s", text))
	_, ok, retryable = f.sendMessage(ctx, client, t, "text", feishuTextContent(text), "")
	return ok, retryable
}

// sendMessage 调用 im/v1/message.create 发送一条消息，统一错误分类。
// reqUUID 非空时设入请求 Uuid 字段实现幂等去重（同一 uuid 一小时内最多成功一次）。
// 返回 (messageID, ok, retryable)：成功时 messageID 非空，供后续 PATCH 流式更新。
func (f *Feishu) sendMessage(ctx context.Context, client *lark.Client, t config.WebhookTarget, msgType, content, reqUUID string) (messageID string, ok, retryable bool) {
	bodyBuilder := larkim.NewCreateMessageReqBodyBuilder().
		ReceiveId(t.ReceiveOpenID).
		MsgType(msgType).
		Content(content)
	if reqUUID != "" {
		bodyBuilder.Uuid(reqUUID)
	}
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType("open_id").
		Body(bodyBuilder.Build()).
		Build()

	f.debug(fmt.Sprintf("开始发送飞书消息（类型：%s）", msgType))
	resp, err := client.Im.V1.Message.Create(ctx, req)
	if err != nil {
		f.debug(fmt.Sprintf("飞书消息发送失败：%v", err))
		return "", false, true // 传输/超时：可重试
	}
	if !resp.Success() {
		// 服务端业务错误：通常为权限/配额/模板配置问题，不重试。
		f.warn("log-feishu-send-failed", map[string]string{
			"logId": resp.RequestId(),
			"code":  strconv.Itoa(resp.Code),
			"msg":   resp.Msg,
		})
		return "", false, false
	}
	f.debug(fmt.Sprintf("飞书消息发送完成（类型：%s，logId=%s）", msgType, resp.RequestId()))
	var msgID string
	if resp.Data != nil && resp.Data.MessageId != nil {
		msgID = *resp.Data.MessageId
	}
	return msgID, true, false
}

// patchMessage 调用 im/v1/message.patch 更新已发送的卡片消息内容。
// 仅更新 content 字段（交互式模板重新渲染）；返回 (ok, retryable)。
func (f *Feishu) patchMessage(ctx context.Context, client *lark.Client, messageID, content string) (ok, retryable bool) {
	req := larkim.NewPatchMessageReqBuilder().
		MessageId(messageID).
		Body(larkim.NewPatchMessageReqBodyBuilder().
			Content(content).
			Build()).
		Build()

	f.debug(fmt.Sprintf("开始更新飞书消息（messageID=%s）", messageID))
	resp, err := client.Im.V1.Message.Patch(ctx, req)
	if err != nil {
		f.debug(fmt.Sprintf("飞书消息更新失败：%v", err))
		return false, true // 传输/超时：可重试
	}
	if !resp.Success() {
		f.warn("log-feishu-send-failed", map[string]string{
			"logId": resp.RequestId(),
			"code":  strconv.Itoa(resp.Code),
			"msg":   resp.Msg,
		})
		return false, false
	}
	f.debug(fmt.Sprintf("飞书消息更新完成（messageID=%s，logId=%s）", messageID, resp.RequestId()))
	return true, false
}

// compressImage 把图片字节流压缩到 ≤ maxBytes。策略：
//  1. 逐步降低 JPEG 质量（95→85→75→65→55）；
//  2. 若仍超限，按 0.85 系数逐级缩小尺寸后重编码（保留长宽比）。
//
// GIF 保持原样返回（imaging 对动图只编首帧，会丢失动画——交给飞书原样处理）。
// 失败（解码/编码出错）时返回错误，由调用方决定是否回退到原图（飞书会拒）。
func compressImage(src []byte, maxBytes int) ([]byte, error) {
	// GIF 不压缩（动图首帧会丢动画）。
	if strings.HasPrefix(http.DetectContentType(src), "image/gif") {
		return src, nil
	}
	img, err := imaging.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	// 第一阶段：降 JPEG 质量，不缩尺寸。
	qualities := []int{95, 85, 75, 65, 55}
	for _, q := range qualities {
		var buf bytes.Buffer
		if err := imaging.Encode(&buf, img, imaging.JPEG, imaging.JPEGQuality(q)); err != nil {
			return nil, fmt.Errorf("encode q=%d: %w", q, err)
		}
		if buf.Len() <= maxBytes {
			return buf.Bytes(), nil
		}
	}

	// 第二阶段：仍超限，逐级缩尺寸（每轮 ×0.85）后再按 75 质量编码。
	for scale := 0.85; ; scale *= 0.85 {
		bounds := img.Bounds()
		w := int(float64(bounds.Dx()) * scale)
		h := int(float64(bounds.Dy()) * scale)
		if w < 2 || h < 2 {
			return nil, fmt.Errorf("cannot shrink below %d bytes", maxBytes)
		}
		resized := imaging.Resize(img, w, h, imaging.Lanczos)
		var buf bytes.Buffer
		if err := imaging.Encode(&buf, resized, imaging.JPEG, imaging.JPEGQuality(75)); err != nil {
			return nil, fmt.Errorf("encode resized %dx%d: %w", w, h, err)
		}
		if buf.Len() <= maxBytes {
			return buf.Bytes(), nil
		}
		// 防止无限循环：尺寸已极小仍超限时退出。
		if w <= 8 || h <= 8 {
			return nil, fmt.Errorf("still %d bytes after max shrink", buf.Len())
		}
	}
}

// feishuTextContent 组装文本消息的 content 字段：{"text":"<文本>"}（JSON 序列化字符串）。
func feishuTextContent(text string) string {
	b, _ := json.Marshal(map[string]string{"text": text})
	return string(b)
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
	f.debug("飞书客户端已初始化")
	return c
}

func (f *Feishu) Prewarm(targets []config.WebhookTarget) {
	count := 0
	for _, t := range targets {
		if t.Type != "feishu" || t.AppID == "" || t.AppSecret == "" {
			continue
		}
		f.larkClient(t.AppID, t.AppSecret)
		count++
	}
	if count > 0 {
		f.debug(fmt.Sprintf("飞书客户端预初始化完成，目标数：%d", count))
	}
}

// uploadImageFromURL 下载指定 URL 的图片，上传到飞书开放平台（image_type=message）
// 返回可填入模板变量的 image_key。
func (f *Feishu) uploadImageFromURL(ctx context.Context, client *lark.Client, imageURL string) (string, error) {
	f.debug("开始下载飞书消息图片")
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
	// 下载硬上限 feishuImageHardLimit（30 MB）：超过则放弃，不下载完整文件。
	// 用 hardLimit+1 检测超限：读到这么多字节说明原图超 30 MB。
	imgBytes, err := io.ReadAll(io.LimitReader(dlResp.Body, feishuImageHardLimit+1))
	if err != nil {
		return "", fmt.Errorf("read image body: %w", err)
	}
	if len(imgBytes) == 0 {
		return "", fmt.Errorf("empty image body")
	}
	if len(imgBytes) > feishuImageHardLimit {
		return "", fmt.Errorf("image too large: %d bytes (hard limit %d)", len(imgBytes), feishuImageHardLimit)
	}
	// 服务端可能返回 application/octet-stream（无扩展名 CDN 文件），且 SDK 的 Image()
	// 只接受 io.Reader、无法传文件名，飞书后端靠内容嗅探识别格式。这里做一次本地嗅探，
	// 及早发现非图片内容（例如 HTML 错误页）以给出清晰错误。
	contentType := http.DetectContentType(imgBytes)
	if !strings.HasPrefix(contentType, "image/") {
		return "", fmt.Errorf("not an image: detected %s", contentType)
	}
	f.debug(fmt.Sprintf("飞书消息图片下载完成（%d 字节，%s）", len(imgBytes), contentType))
	// 以原图字节内容的 sha256 为缓存键：同一张原图（无论从哪个 URL 下载、是否压缩）
	// 在飞书租户内可复用同一 image_key。命中缓存直接返回，跳过压缩+上传整个开销链。
	hash := sha256.Sum256(imgBytes)
	hashHex := hex.EncodeToString(hash[:])
	f.imgMu.Lock()
	if key, ok := f.imgCache[hashHex]; ok {
		f.imgMu.Unlock()
		f.debug(fmt.Sprintf("飞书图片缓存命中，img_key=%s", key))
		return key, nil
	}
	f.imgMu.Unlock()

	// 压缩在缓存未命中后进行；压缩结果同样确定，但用原图哈希即可命中，无需再算一次。
	uploadBytes := imgBytes
	// 软限 feishuImageMaxBytes（10 MB）：超过则本地压缩到 ≤10 MB 再上传，避免被飞书 234006 拒。
	if len(imgBytes) > feishuImageMaxBytes {
		uploadBytes, err = compressImage(imgBytes, feishuImageMaxBytes)
		if err != nil {
			return "", fmt.Errorf("compress image: %w", err)
		}
		f.debug(fmt.Sprintf("飞书图片压缩完成（%d → %d 字节）", len(imgBytes), len(uploadBytes)))
	}

	upReq := larkim.NewCreateImageReqBuilder().
		Body(larkim.NewCreateImageReqBodyBuilder().
			ImageType("message").
			Image(bytes.NewReader(uploadBytes)).
			Build()).
		Build()
	f.debug(fmt.Sprintf("开始上传飞书图片（%d 字节）", len(uploadBytes)))
	upResp, err := client.Im.V1.Image.Create(ctx, upReq)
	if err != nil {
		return "", fmt.Errorf("upload image: %w", err)
	}
	if !upResp.Success() {
		// 附带 logId 便于在飞书开放平台后台定位请求（如 234006 超限 / 234011 格式不支持 / 234039 分辨率超限）。
		return "", fmt.Errorf("upload image code=%d msg=%s logId=%s", upResp.Code, upResp.Msg, upResp.RequestId())
	}
	if upResp.Data == nil || upResp.Data.ImageKey == nil {
		return "", fmt.Errorf("upload image: no image_key in response")
	}
	imageKey := *upResp.Data.ImageKey
	// 上传成功后写回缓存（以原图哈希为键）。
	f.imgMu.Lock()
	f.imgCache[hashHex] = imageKey
	f.imgMu.Unlock()
	f.debug(fmt.Sprintf("飞书图片上传完成（img_key=%s，logId=%s）", imageKey, upResp.RequestId()))
	return imageKey, nil
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

// feishuTemplateContentRaw 用指定 templateID 组装模板信封，供不同事件使用不同模板时调用。
func feishuTemplateContentRaw(templateID, templateVersion string, vars map[string]any) string {
	envelope := map[string]any{
		"type": "template",
		"data": map[string]any{
			"template_id":           templateID,
			"template_version_name": templateVersion,
			"template_variable":     vars,
		},
	}
	b, _ := json.Marshal(envelope)
	return string(b)
}
