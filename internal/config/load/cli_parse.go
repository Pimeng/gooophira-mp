package load

import (
	"strconv"
	"strings"
)

// 入口层（cmd/server）解析命令行参数时复用配置字段的解析/校验规则（与 env、YAML
// 文件同一套逻辑），失败时由调用方按启动期语言本地化报错。对齐 TS main 的 requireParse。

// ParsePort 校验端口号（整数，1..65535）。非法返回 ok=false。
func ParsePort(v any) (int, bool) { return parsePortValue(v) }

// ParseRoomMaxUsers 校验房间最大用户数（整数，>=1，上限 32767）。非法返回 ok=false。
func ParseRoomMaxUsers(v any) (int, bool) { return parseRoomMaxUsersValue(v) }

// ParseIntegerList 解析逗号/空白/分号分隔的整数列表（如 MONITORS）。空或无有效项返回 ok=false。
func ParseIntegerList(v any) ([]int, bool) { return parseIntegerListValue(v) }

// ParseBool 解析布尔值（true/false/yes/no/on/off/1/0，大小写不敏感）。非法返回 ok=false。
func ParseBool(v any) (bool, bool) { return parseBoolValue(v) }

// ParseString 解析非空字符串（去除首尾空白）。空白串返回 ok=false。
func ParseString(v any) (string, bool) { return parseStringValue(v) }

// ParseInteger 解析非负整数（用于毫秒级时间、序列号等无单位数值）。空或非法返回 ok=false。
func ParseInteger(v any) (int, bool) {
	s, ok := v.(string)
	if !ok {
		return 0, false
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}
