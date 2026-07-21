package load

import (
	"strings"
)

func parseWebhookValue(v any) (*WebhookConfig, bool) {
	m, ok := asRecord(v)
	if !ok {
		return nil, false
	}
	enabled, _ := parseBoolValue(m["ENABLED"]) // 缺省/非法 → false（显式 opt-in）
	timeoutMS := 0
	if n, ok := asInt(m["TIMEOUT_MS"]); ok && n > 0 {
		timeoutMS = n
	}
	retries := -1
	if n, ok := asInt(m["RETRIES"]); ok && n >= 0 {
		retries = n
	}

	var targets []WebhookTarget
	if rawList, present := m["TARGETS"]; present {
		if list, ok := rawList.([]any); ok {
			for _, item := range list {
				tm, ok := asRecord(item)
				if !ok {
					continue
				}
				typ, _ := parseStringValue(tm["TYPE"])
				typ = strings.ToLower(typ)
				switch typ {
				case "onebotv11", "onebot-v11":
					typ = "onebot_v11"
				}
				if typ == "" {
					typ = "generic"
				}
				id, _ := parseStringValue(tm["ID"])
				events, _ := parseStringListValue(tm["EVENTS"]) // nil = 订阅全部

				if typ == "feishu" {
					// 飞书 SDK 目标：校验应用凭据与接收人。模板 ID 可选覆盖，留空走内置默认。
					appID, _ := parseStringValue(tm["APP_ID"])
					appSecret, _ := parseStringValue(tm["APP_SECRET"])
					receiveOpenID, _ := parseStringValue(tm["RECEIVE_OPEN_ID"])
					if appID == "" || appSecret == "" || receiveOpenID == "" {
						continue // 无效目标：缺少飞书必填字段，跳过
					}
					templateID, _ := parseStringValue(tm["TEMPLATE_ID"])
					templateVersion, _ := parseStringValue(tm["TEMPLATE_VERSION"])
					gameEndTemplateID, _ := parseStringValue(tm["GAME_END_TEMPLATE_ID"])
					gameEndTemplateVersion, _ := parseStringValue(tm["GAME_END_TEMPLATE_VERSION"])
					liveUpdate, _ := parseBoolValue(tm["LIVE_UPDATE"])
					targets = append(targets, WebhookTarget{
						ID:                     id,
						Type:                   typ,
						Events:                 events,
						AppID:                  appID,
						AppSecret:              appSecret,
						ReceiveOpenID:          receiveOpenID,
						TemplateID:             templateID,
						TemplateVersion:        templateVersion,
						GameEndTemplateID:      gameEndTemplateID,
						GameEndTemplateVersion: gameEndTemplateVersion,
						LiveUpdate:             liveUpdate,
					})
					continue
				}

				if typ == "onebot_v11" {
					url, okURL := parseStringValue(tm["URL"])
					messageType, _ := parseStringValue(tm["MESSAGE_TYPE"])
					messageType = strings.ToLower(messageType)
					targetIDs, okTargetID := parseIntegerListValue(tm["TARGET_ID"])
					if !okURL || (messageType != "private" && messageType != "group") || !okTargetID {
						continue
					}
					ids := make([]int64, len(targetIDs))
					validTargetIDs := true
					for i, targetID := range targetIDs {
						if targetID <= 0 {
							validTargetIDs = false
							break
						}
						ids[i] = int64(targetID)
					}
					if !validTargetIDs {
						continue
					}
					accessToken, _ := parseStringValue(tm["ACCESS_TOKEN"])
					targets = append(targets, WebhookTarget{
						ID:          id,
						URL:         url,
						Type:        typ,
						Events:      events,
						AccessToken: accessToken,
						MessageType: messageType,
						TargetID:    ids[0],
						TargetIDs:   ids,
					})
					continue
				}

				// HTTP 类目标：缺 URL 跳过。
				url, okURL := parseStringValue(tm["URL"])
				if !okURL {
					continue
				}
				secret, _ := parseStringValue(tm["SECRET"])
				targets = append(targets, WebhookTarget{ID: id, URL: url, Type: typ, Events: events, Secret: secret})
			}
		}
	}

	return &WebhookConfig{Enabled: enabled, TimeoutMS: timeoutMS, Retries: retries, Targets: targets}, true
}
