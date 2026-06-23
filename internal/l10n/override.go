package l10n

import (
	"maps"
	"os"
	"path/filepath"
)

// LoadOverrides 从 dir 读取 <lang>.ftl 覆盖文件，仅覆盖文件中出现的键，其余键沿用内置翻译。
//
// 供服主自定义部分文案（如欢迎语、提示）。必须在启动期、对外服务前调用，因其会就地修改
// 全局 bundles（之后 TL 并发读取无锁）。dir 为空或文件缺失时静默跳过。返回成功加载覆盖的语言数。
func LoadOverrides(dir string) int {
	if dir == "" {
		return 0
	}
	ensureBundles()
	loaded := 0
	for _, lang := range supportedLangs {
		data, err := os.ReadFile(filepath.Join(dir, lang+".ftl"))
		if err != nil {
			continue // 该语言无覆盖文件
		}
		override := parseResource(string(data))
		if len(override) == 0 {
			continue
		}
		maps.Copy(bundles[lang], override) // 覆盖文件中出现的键，其余沿用内置
		loaded++
	}
	return loaded
}
