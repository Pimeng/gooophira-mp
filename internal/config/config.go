package config

import (
	"math"
	"strconv"
	"strings"
)

// ---------- 解析助手（接受 env 字符串或 YAML 原生类型） ----------

func asFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

func asInt(v any) (int, bool) {
	f, ok := asFloat(v)
	if !ok || f != math.Trunc(f) {
		return 0, false
	}
	return int(f), true
}

func parseBoolValue(v any) (bool, bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case int:
		if x == 1 {
			return true, true
		}
		if x == 0 {
			return false, true
		}
		return false, false
	case float64:
		if x == 1 {
			return true, true
		}
		if x == 0 {
			return false, true
		}
		return false, false
	case string:
		s := strings.ToLower(strings.TrimSpace(x))
		switch s {
		case "1", "true", "yes", "on":
			return true, true
		case "0", "false", "no", "off":
			return false, true
		}
		return false, false
	default:
		return false, false
	}
}

func parseStringValue(v any) (string, bool) {
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	t := strings.TrimSpace(s)
	if t == "" {
		return "", false
	}
	return t, true
}

func parsePortValue(v any) (int, bool) {
	n, ok := asInt(v)
	if !ok || n <= 0 || n > 65535 {
		return 0, false
	}
	return n, true
}

// logLevels 接受 4 字符规范名（DEBU/INFO/MARK/WARN/ERRO）与 5 字符旧别名
// （DEBUG/ERROR），统一规范化为短形式后落盘与下发，详见 parseLogLevelValue。
var logLevels = map[string]string{
	"DEBU":  "DEBU",
	"DEBUG": "DEBU",
	"INFO":  "INFO",
	"MARK":  "MARK",
	"WARN":  "WARN",
	"ERRO":  "ERRO",
	"ERROR": "ERRO",
}

func parseLogLevelValue(v any) (string, bool) {
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	u := strings.ToUpper(strings.TrimSpace(s))
	norm, ok := logLevels[u]
	if u == "" || !ok {
		return "", false
	}
	return norm, true
}

func parseRoomMaxUsersValue(v any) (int, bool) {
	n, ok := asInt(v)
	if !ok || n < 1 {
		return 0, false
	}
	return min(n, MaxRoomMaxUsers), true
}

func parseReplayTTLDaysValue(v any) (int, bool) {
	n, ok := asInt(v)
	if !ok || n < 1 {
		return 0, false
	}
	return min(n, MaxReplayTTLDays), true
}

func parsePositiveIntValue(v any) (int, bool) {
	n, ok := asInt(v)
	if !ok || n < 1 {
		return 0, false
	}
	return n, true
}

func parseNonNegativeIntValue(v any) (int, bool) {
	n, ok := asInt(v)
	if !ok || n < 0 {
		return 0, false
	}
	return n, true
}

func parsePlayingGraceValue(v any) (int, bool) {
	n, ok := parseNonNegativeIntValue(v)
	if !ok {
		return 0, false
	}
	return min(n, MaxPlayingReconnectGrace), true
}

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
// 仅当 v 根本不是 map 时返回 false（视为未设置）。逐个目标解析：缺 URL 的目标跳过。
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
				url, okURL := parseStringValue(tm["URL"])
				if !okURL {
					continue // 无效目标：缺 URL，跳过
				}
				typ, _ := parseStringValue(tm["TYPE"])
				typ = strings.ToLower(typ)
				if typ == "" {
					typ = "generic"
				}
				events, _ := parseStringListValue(tm["EVENTS"]) // nil = 订阅全部
				secret, _ := parseStringValue(tm["SECRET"])
				targets = append(targets, WebhookTarget{URL: url, Type: typ, Events: events, Secret: secret})
			}
		}
	}

	return &WebhookConfig{Enabled: enabled, TimeoutMS: timeoutMS, Retries: retries, Targets: targets}, true
}
