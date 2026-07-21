package load

import (
	"os"
	"path/filepath"
)

// DefaultConfigYAML 是自动生成默认配置时的兜底内容（仅当本地示例 server_config.example.yml
// 缺失时使用，如精简打包场景）。对齐 TS core/server.ts 的 DEFAULT_CONFIG_YAML。
const DefaultConfigYAML = `# Phira MP 服务端配置（首次启动自动生成）
# 这是已弃用的单文件兼容配置；新部署使用 config/server.yaml。
HOST: "::"
PORT: 12346
HTTP_SERVICE: false
HTTP_PORT: 12347
LOG_LEVEL: INFO
ROOM_MAX_USERS: 512
MONITORS:
  - 2
`

// EnsureDefaultFile 在首次运行未找到配置文件时自动生成一份默认配置，避免服主在不知情下
// 全程使用内存默认值。来源优先级：本地带注释示例（与目标配置同目录的
// server_config.example.yml）> 内置无注释最小模板。
//
// 文件已存在则原样返回、不覆盖；成功生成返回 created=true。任何失败（如只读文件系统）返回
// err，由调用方继续用内存默认值（不致命，对齐 TS ensureDefaultConfigFile 的 catch→null）。
//
// 注：原 TS 在「本地示例」与「内置模板」之间还有一档「在线拉取 GitHub release 示例」，但其
// URL 指向上游 tphira 的发布镜像、且 Go 版已 embed 全部语言（locales 在线补齐对 Go 不适用），
// 故此处不引入启动期出站网络请求——仅本地示例 + 内置模板。
func EnsureDefaultFile(configPath string) (created bool, err error) {
	if _, statErr := os.Stat(configPath); statErr == nil {
		return false, nil // 已存在，不动
	} else if !os.IsNotExist(statErr) {
		return false, statErr // 其它 stat 错误（权限等）
	}

	// 1. 本地完整示例（仓库 / 开发运行时存在），离线且与当前工作副本一致。
	example := filepath.Join(filepath.Dir(configPath), "server_config.example.yml")
	if data, rerr := os.ReadFile(example); rerr == nil && len(data) > 0 {
		if werr := os.WriteFile(configPath, data, 0o644); werr != nil {
			return false, werr
		}
		return true, nil
	}

	// 2. 兜底：内置无注释最小模板。
	if werr := os.WriteFile(configPath, []byte(DefaultConfigYAML), 0o644); werr != nil {
		return false, werr
	}
	return true, nil
}
