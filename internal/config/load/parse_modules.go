package load

import (
	"strings"
)

func parseOutboundProxyValue(v any) (*OutboundProxy, bool) {
	switch x := v.(type) {
	case bool:
		if !x {
			return &OutboundProxy{Direct: true}, true
		}
		return nil, false
	case string:
		t := strings.TrimSpace(x)
		if t == "" {
			return nil, false
		}
		if strings.ToLower(t) == "false" {
			return &OutboundProxy{Direct: true}, true
		}
		return &OutboundProxy{URL: t}, true
	default:
		return nil, false
	}
}

func asRecord(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func parseShareStationValue(v any) (*ShareStation, bool) {
	m, ok := asRecord(v)
	if !ok {
		return nil, false
	}
	url, okURL := parseStringValue(m["URL"])
	token, okTok := parseStringValue(m["TOKEN"])
	if !okURL || !okTok {
		return nil, false
	}
	return &ShareStation{URL: url, Token: token}, true
}

func parseRedisValue(v any) (*RedisConfig, bool) {
	m, ok := asRecord(v)
	if !ok {
		return nil, false
	}
	enabled, okEn := parseBoolValue(m["ENABLED"])
	if !okEn {
		return nil, false
	}
	host, okHost := parseStringValue(m["HOST"])
	if !okHost {
		host = "127.0.0.1"
	}
	port, okPort := parsePortValue(m["PORT"])
	if !okPort {
		port = 6379
	}
	password, _ := parseStringValue(m["PASSWORD"])
	db := 0
	if raw, present := m["DB"]; present {
		if n, ok := asInt(raw); ok && n >= 0 {
			db = n
		} else if raw != nil && raw != "" {
			return nil, false
		}
	}
	return &RedisConfig{Enabled: enabled, Host: host, Port: port, Password: password, DB: db}, true
}

// parseNetutilValue 解析 NETUTIL 块。仅当 v 根本不是 map 时返回 false（视为未设置）。
// DNS_SERVERS 为空或全空白时仍返回结构体，但 EffectiveDNSServers 会回退到内置默认。

func parseNetutilValue(v any) (*NetutilConfig, bool) {
	m, ok := asRecord(v)
	if !ok {
		return nil, false
	}
	servers, _ := parseStringListValue(m["DNS_SERVERS"])
	return &NetutilConfig{DNSServers: servers}, true
}

// parseWebhookValue 解析 WEBHOOK 块。结构合法即返回（即便 ENABLED 缺省为 false / TARGETS 为空），
// 仅当 v 根本不是 map 时返回 false（视为未设置）。逐个目标解析：
//   - Type=generic/discord：缺 URL 的目标跳过。
//   - Type=onebot_v11：校验 URL、MESSAGE_TYPE（private/group）与正数 TARGET_ID（单值或数组）；ACCESS_TOKEN 可选。
//   - Type=feishu：走飞书开放平台 SDK，URL 不再使用，改为校验 AppID/AppSecret/ReceiveOpenID。模板 ID 可选覆盖，留空走内置默认。
