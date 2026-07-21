package load

import (
	"errors"
	"os"
	"path/filepath"
)

const MinimalServerYAML = `# Phira MP 核心配置文件（首次启动自动生成）
# 未写出的项目使用内置默认值；完整中文说明见 config.example/server.yaml。
# 将 replay.yaml、redis.yaml 或 network.yaml 放到本文件旁边，才会启用
# 对应的 server 扩展；Agent 扩展统一配置在 agent.yaml。
# 配置优先级：命令行参数 > 环境变量 > 本文件 > 内置默认值。

# 配置格式版本。必须保留，当前只支持 1。
version: 1

# TCP 游戏服务监听地址；"::" 表示监听所有 IPv6 地址，通常也兼容 IPv4。
HOST: "::"
# TCP 游戏服务监听端口。
PORT: 12346
# 显示给玩家的服务器名称。
SERVER_NAME: "Phira MP"
# 服务端语言，影响日志、CLI 和 HTTP 默认输出。
LANG: "zh-CN"

# 可选 Agent 默认关闭。启用时可设为 auto，Agent 不在线不影响主程序。
# AGENT_IPC:
#   ENDPOINT: auto
`

// EnsureConfigDir 只创建必需的最小 server.yaml。
// 可选文件的存在会启用功能，因此这里刻意不复制它们。

func EnsureConfigDir(dir string) (bool, error) {
	path := filepath.Join(dir, CoreConfigFile)
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, []byte(MinimalServerYAML), 0o644); err != nil {
		return false, err
	}
	return true, nil
}
