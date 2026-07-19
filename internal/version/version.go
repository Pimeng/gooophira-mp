// Package version 提供应用版本号（唯一来源）。
//
// 优先级：PHIRA_MP_VERSION 环境变量 > ldflags 注入 > 内嵌 VERSION 文件 > 构建信息 > "dev"。
//
// 普通 `go build` 即得到 VERSION 文件里的版本（当前 0.0.13）。release 构建可用 ldflags 覆盖，
// 例如注入 git 描述：
//
// 构建示例：
//
//	go build -ldflags "-X github.com/Pimeng/gooophira-mp/internal/version.injected=$(git describe --tags --always)" ./cmd/server
package version

import (
	_ "embed"
	"os"
	"runtime/debug"
	"strings"
)

//go:embed VERSION
var embedded string

// injected 由 ldflags 在构建期注入（默认空，留给 release 流水线）。
var injected string

// Get 返回应用版本号。
func Get() string {
	if v := strings.TrimSpace(os.Getenv("PHIRA_MP_VERSION")); v != "" {
		return v
	}
	if v := strings.TrimSpace(injected); v != "" {
		return v
	}
	if v := strings.TrimSpace(embedded); v != "" {
		return v
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := strings.TrimSpace(bi.Main.Version); v != "" && v != "(devel)" {
			return v
		}
	}
	return "dev"
}
