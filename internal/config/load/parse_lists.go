package load

import (
	"regexp"
	"strings"
)

var listSepInt = regexp.MustCompile(`[,\s;，]+`)
var listSepStr = regexp.MustCompile(`[,\s;]+`)

func parseIntegerListValue(v any) ([]int, bool) {
	switch x := v.(type) {
	case []any:
		out := make([]int, 0, len(x))
		for _, it := range x {
			if n, ok := asInt(it); ok {
				out = append(out, n)
			}
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, true
	case []int:
		if len(x) == 0 {
			return nil, false
		}
		return x, true
	case string:
		if strings.TrimSpace(x) == "" {
			return nil, false
		}
		var out []int
		for _, part := range listSepInt.Split(x, -1) {
			if n, ok := asInt(strings.TrimSpace(part)); ok {
				out = append(out, n)
			}
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, true
	default:
		if n, ok := asInt(v); ok {
			return []int{n}, true
		}
		return nil, false
	}
}

func parseStringListValue(v any) ([]string, bool) {
	switch x := v.(type) {
	case []any:
		var out []string
		for _, it := range x {
			if s, ok := it.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, true
	case []string:
		var out []string
		for _, s := range x {
			if strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, true
	case string:
		if strings.TrimSpace(x) == "" {
			return nil, false
		}
		var out []string
		for _, part := range listSepStr.Split(x, -1) {
			if t := strings.TrimSpace(part); t != "" {
				out = append(out, t)
			}
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, true
	default:
		return nil, false
	}
}
