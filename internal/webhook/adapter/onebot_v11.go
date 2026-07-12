// OneBot v11 适配器：经 HTTP API 向 QQ 私聊或群聊发送纯文本消息。
package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/server"
)

// OneBotV11 是 OneBot v11 HTTP API 适配器。
type OneBotV11 struct {
	client *http.Client
}

// NewOneBotV11 创建 OneBot v11 适配器。
func NewOneBotV11(httpClient *http.Client) *OneBotV11 {
	return &OneBotV11{client: httpClient}
}

type oneBotResponse struct {
	Status  string `json:"status"`
	RetCode int    `json:"retcode"`
}

// Deliver 使用 send_private_msg 或 send_group_msg 发送内置格式的纯文本消息。
func (o *OneBotV11) Deliver(ctx context.Context, t config.WebhookTarget, ev server.Event) (ok, retryable bool) {
	action, idField := "send_private_msg", "user_id"
	if t.MessageType == "group" {
		action, idField = "send_group_msg", "group_id"
	} else if t.MessageType != "private" {
		return false, false
	}

	body, err := json.Marshal(map[string]any{
		idField:       t.TargetID,
		"message":     RenderText(ev),
		"auto_escape": true,
	})
	if err != nil {
		return false, false
	}

	endpoint, err := url.JoinPath(t.URL, action)
	if err != nil {
		return false, false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return false, false
	}
	req.Header.Set("Content-Type", ctJSON)
	req.Header.Set("User-Agent", userAgent)
	if t.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+t.AccessToken)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return false, true
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return false, resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
	}

	var result oneBotResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return false, false
	}
	return result.Status == "ok" && result.RetCode == 0, false
}
