package load

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// 配置文件持久化（保留注释）。对应 TS core/configPersist.ts。
//
// 运行时通过 CLI / HTTP admin 改动少量「可热切换」的顶层标量配置后，需把新值落盘以便
// 重启保持。为避免整体 dump 抹掉注释/空行/键顺序，这里逐行原地更新：只替换目标键所在
// 那一行的值，其余字节原样保留；键不存在则在文件末尾追加。仅处理顶层扁平标量键。

// serializeScalar 把标量值序列化为 YAML 行内表示。
func serializeScalar(v any) string {
	switch x := v.(type) {
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case string:
		// 双引号包裹并转义，避免 YAML 把 yes/no/数字样式的字符串误解析。
		b, _ := json.Marshal(x)
		return string(b)
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

// ApplyConfigUpdates 在配置文本中就地更新（或追加）若干顶层标量键，返回新文本。
//
//   - 仅匹配行首无缩进的 `KEY:`（顶层键），不会误伤嵌套块里的同名子键。
//   - 命中已存在的活动行时只替换其值（首个匹配），保留该行之外的一切。
//   - 被注释掉的示例行（`# KEY: ...`）不算命中，此时在文件末尾追加活动行。
//   - 保留原换行风格：只重写键所在行的「键:值」部分。
func ApplyConfigUpdates(text string, updates map[string]any) string {
	out := text
	// 排序键以保证追加顺序与输出确定（便于测试与稳定 diff）。
	keys := make([]string, 0, len(updates))
	for k := range updates {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		serialized := key + ": " + serializeScalar(updates[key])
		re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `:[^\r\n]*`)
		if loc := re.FindStringIndex(out); loc != nil {
			out = out[:loc[0]] + serialized + out[loc[1]:] // 仅替换首个匹配（对齐 TS String.replace）
		} else {
			if len(out) > 0 && !strings.HasSuffix(out, "\n") {
				out += "\n"
			}
			out += serialized + "\n"
		}
	}
	return out
}

// PersistConfigValues 把若干顶层标量配置项写回配置文件，保留注释与格式。
// 采用「写临时文件再 rename」的原子写入；rename 失败（如 Windows 文件锁）时退化为
// 先删后改名重试一次。配置文件不存在时以空内容为基底。无实际变化则直接返回。
func PersistConfigValues(configPath string, updates map[string]any) error {
	original, err := os.ReadFile(configPath)
	if err != nil {
		original = nil // 文件不存在/读失败：以空内容为基底
	}
	next := ApplyConfigUpdates(string(original), updates)
	if next == string(original) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return err
	}
	tmp := configPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(next), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, configPath); err != nil {
		_ = os.Remove(configPath) // 文件可能不存在
		return os.Rename(tmp, configPath)
	}
	return nil
}

// PersistConfigDirValues 把运行时更新写入各键所属的文件。
// 只有显式运行时更新指向某个可选文件时，才会创建该文件。
func PersistConfigDirValues(configDir string, updates map[string]any) error {
	byFile := make(map[string]map[string]any)
	for key, value := range updates {
		name := configFileForKey(key)
		if byFile[name] == nil {
			byFile[name] = make(map[string]any)
		}
		byFile[name][key] = value
	}
	for name, fileUpdates := range byFile {
		path := filepath.Join(configDir, name)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			if name == CoreConfigFile {
				return fmt.Errorf("required configuration file does not exist: %s", path)
			}
			if err := os.WriteFile(path, []byte("version: 1\n"), 0o644); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if err := PersistConfigValues(path, fileUpdates); err != nil {
			return err
		}
	}
	return nil
}
