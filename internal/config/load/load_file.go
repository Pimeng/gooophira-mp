package load

import (
	"errors"
	"os"

	"gopkg.in/yaml.v3"
)

// LoadFile 从 YAML 配置文件解析配置（键为大写 ENV 名）。
// yaml.v3 把映射解析为 map[string]any、序列为 []any、标量为 int/bool/string/float64，
// 正好与 BuildFromMap 的解析助手匹配（含嵌套 SHARE_STATION / REDIS 块）。
func LoadFile(path string) (*ServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return BuildFromMap(m), nil
}

// LoadMerged 加载并合并配置，优先级 env > 文件。文件不存在时退化为仅 env + 默认值。
// 返回 (配置, 是否从文件加载, error)。
func LoadMerged(path string) (cfg *ServerConfig, fromFile bool, err error) {
	env := LoadEnv()
	fileCfg, ferr := LoadFile(path)
	if ferr != nil {
		if errors.Is(ferr, os.ErrNotExist) {
			return Merge(&ServerConfig{}, env), false, nil
		}
		return nil, false, ferr
	}
	// Merge(base, override)：override 覆盖 base，故 env 覆盖文件。
	return Merge(fileCfg, env), true, nil
}
