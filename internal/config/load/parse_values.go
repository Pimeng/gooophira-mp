package load

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
