// Package guiwindow 在服务端启动时弹出一个独立的浏览器「应用模式」窗口承载 GUI 控制台
// （类似 Minecraft 服务端 GUI）。复用系统已安装的 Edge/Chrome 的 --app 模式，得到无地址栏、
// 带独立任务栏图标的窗口，零新增依赖，与单文件打包/容器部署兼容。
//
// 探测顺序：Edge → Chrome →（Linux 另试 Chromium）→ 系统默认浏览器兜底。
// 找不到任何浏览器时 Launch 返回 false，由调用方提示手动访问。对应 TS gui/guiWindow.ts。
package guiwindow

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// profileDir 是应用模式窗口的专用浏览器配置目录（与日常浏览器隔离，并记住窗口大小）。
func profileDir() string {
	return filepath.Join(os.TempDir(), "phira-mp-gui-profile")
}

// appModeArgs 是 Chromium 系应用模式启动参数。
func appModeArgs(url string) []string {
	return []string{
		"--app=" + url,
		"--user-data-dir=" + profileDir(),
		"--no-first-run",
		"--no-default-browser-check",
		"--window-size=1380,860",
	}
}

// tryLaunch 启动一个独立（detached）进程；通过 Start 是否报错判断可执行文件是否存在。
// 成功拉起返回 true，且不等待——进程独立于服务器生命周期。
func tryLaunch(name string, args ...string) bool {
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil // 不继承 stdio
	if err := cmd.Start(); err != nil {
		return false
	}
	_ = cmd.Process.Release() // 不回收，随它去
	return true
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// windowsCandidates 返回 Windows 下常见的 Edge / Chrome 安装路径。
func windowsCandidates() []string {
	bases := []string{
		envOr("ProgramFiles(x86)", `C:\Program Files (x86)`),
		envOr("ProgramFiles", `C:\Program Files`),
		os.Getenv("LOCALAPPDATA"),
	}
	suffixes := []string{
		`Microsoft\Edge\Application\msedge.exe`,
		`Google\Chrome\Application\chrome.exe`,
	}
	var out []string
	for _, suf := range suffixes {
		for _, base := range bases {
			if base != "" {
				out = append(out, filepath.Join(base, suf))
			}
		}
	}
	return out
}

// Launch 打开服务端 GUI 窗口。返回是否成功拉起（false 表示需提示用户手动访问 URL）。
func Launch(url string) bool {
	switch runtime.GOOS {
	case "windows":
		for _, exe := range windowsCandidates() {
			if fileExists(exe) && tryLaunch(exe, appModeArgs(url)...) {
				return true
			}
		}
		// 兜底：系统默认浏览器（普通标签页）。rundll32 无 shell 引号问题，可完整传递 # 片段。
		return tryLaunch("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		// open -na 在应用不存在时以非零码退出但 spawn 本身成功，故优先探测应用路径。
		for _, app := range []string{"Microsoft Edge", "Google Chrome"} {
			if !fileExists("/Applications/" + app + ".app") {
				continue
			}
			args := append([]string{"-na", app, "--args"}, appModeArgs(url)...)
			if tryLaunch("open", args...) {
				return true
			}
		}
		return tryLaunch("open", url)
	default:
		// Linux / 其它：依次尝试 PATH 中的浏览器。
		for _, bin := range []string{"microsoft-edge", "google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
			if tryLaunch(bin, appModeArgs(url)...) {
				return true
			}
		}
		return tryLaunch("xdg-open", url)
	}
}
