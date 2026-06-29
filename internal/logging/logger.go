// Package logging 提供 server.Logger 的标准输出实现，日志格式与配色对齐原版。
//
// 行格式：`[YYYY-MM-DD HH:MM:SS.mmm] [LEVEL] message`；按级别配色（终端且未设 NO_COLOR 时）；
// WARN/ERROR 走 stderr，其余 stdout。同时写入 `<logsDir>/<date>.log`（按日轮转，追加）。
//
// 旧日志 gzip 压缩 + 总容量上限见 maintenance.go。SetOnLog 旁路供 GUI 控制台缓冲。
package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Level 是日志级别。
type Level int

// 级别枚举（权重见 weight）。
const (
	LevelDebug Level = iota
	LevelInfo
	LevelMark
	LevelWarn
	LevelError
)

func parseLevel(s string) Level {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DEBUG":
		return LevelDebug
	case "MARK":
		return LevelMark
	case "WARN":
		return LevelWarn
	case "ERROR":
		return LevelError
	default:
		return LevelInfo
	}
}

func (l Level) label() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelMark:
		return "MARK"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "INFO"
	}
}

// ANSI 颜色（对齐原版：DEBUG 蓝 / INFO 绿 / MARK 灰 / WARN 黄 / ERROR 红）。
func (l Level) color() string {
	switch l {
	case LevelDebug:
		return "\x1b[34m"
	case LevelMark:
		return "\x1b[90m"
	case LevelWarn:
		return "\x1b[33m"
	case LevelError:
		return "\x1b[31m"
	default:
		return "\x1b[32m"
	}
}

const colorReset = "\x1b[0m"

// Logger 是写控制台 + 日志文件的分级日志器，满足 server.Logger。
// min 用原子存储以支持运行时热调整级别（SetLevel）而不与 log 读竞争。
type Logger struct {
	mu          sync.Mutex
	min         atomic.Int32 // 存 Level
	useColor    bool
	logsDir     string
	file        *os.File
	currentDate string
	connRL      *connRateLimiter                     // 连接日志限流 / IP 黑名单
	onLog       atomic.Pointer[func(string, string)] // 每条日志的旁路回调（GUI 控制台缓冲）
}

// New 按最小级别（如 "INFO"）创建 Logger，并把日志按日轮转写入 logsDir。
// logsDir 为空则仅输出控制台。
func New(minLevel, logsDir string) *Logger {
	l := &Logger{useColor: detectColor(), logsDir: logsDir, connRL: newConnRateLimiter()}
	l.min.Store(int32(parseLevel(minLevel)))
	return l
}

// ConnectionLog 记录一条「新连接」日志（debug 级），并对来源 IP 做频率抑制：单 IP 过于频繁时
// 把其拉入黑名单，期内连接日志静默丢弃，防止日志洪水。ip 为空时不做抑制。
func (l *Logger) ConnectionLog(ip, msg string) {
	if ip != "" && !l.connRL.shouldLog(ip) {
		return // 黑名单内或超频：抑制此日志
	}
	l.log(LevelDebug, msg)
}

// GetBlacklistedIPs 返回当前被连接日志限流拉黑的 IP 列表（供 CLI ipblacklist list）。
func (l *Logger) GetBlacklistedIPs() []BlacklistedIP { return l.connRL.getBlacklisted() }

// RemoveFromBlacklist 手动解封某 IP。
func (l *Logger) RemoveFromBlacklist(ip string) { l.connRL.remove(ip) }

// ClearBlacklist 清空连接日志黑名单。
func (l *Logger) ClearBlacklist() { l.connRL.clear() }

// SetLevel 运行时调整最小输出级别（配置热重载用）。无法识别的级别回退 INFO。
func (l *Logger) SetLevel(minLevel string) { l.min.Store(int32(parseLevel(minLevel))) }

// SetOnLog 注册每条日志的旁路回调（level, msg），用于把日志喂给 GUI 控制台缓冲。
func (l *Logger) SetOnLog(fn func(level, msg string)) { l.onLog.Store(&fn) }

// Close 关闭当前日志文件。
func (l *Logger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
}

// writeFile 把（无色）行写入按日轮转的日志文件（调用方须持 mu）。
func (l *Logger) writeFile(line string) {
	if l.logsDir == "" {
		return
	}
	dateKey := time.Now().Format("2006-01-02")
	if l.file == nil || l.currentDate != dateKey {
		if l.file != nil {
			_ = l.file.Close()
			l.file = nil
		}
		if err := os.MkdirAll(l.logsDir, 0o755); err != nil {
			return
		}
		f, err := os.OpenFile(filepath.Join(l.logsDir, dateKey+".log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		l.file, l.currentDate = f, dateKey
	}
	fmt.Fprintln(l.file, line)
}

func detectColor() bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	return isTerminal(os.Stdout)
}

// isTerminal 跨平台判断是否字符设备（终端），无需外部依赖。
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func (l *Logger) log(level Level, msg string) {
	if level < Level(l.min.Load()) {
		return
	}
	if fn := l.onLog.Load(); fn != nil {
		(*fn)(level.label(), msg) // 旁路到 GUI 控制台缓冲
	}
	line := fmt.Sprintf("[%s] [%s] %s", time.Now().Format("2006-01-02 15:04:05.000"), level.label(), msg)

	out := os.Stdout
	if level == LevelWarn || level == LevelError {
		out = os.Stderr
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.useColor {
		fmt.Fprintf(out, "%s%s%s\n", level.color(), line, colorReset)
	} else {
		fmt.Fprintln(out, line)
	}
	l.writeFile(line) // 文件始终写无色行
}

// DebugEnabled 报告 DEBUG 级别是否启用（供热路径短路，避免无谓的格式化与分配）。
func (l *Logger) DebugEnabled() bool { return Level(l.min.Load()) <= LevelDebug }

// Debug 记录调试级日志。
func (l *Logger) Debug(msg string) { l.log(LevelDebug, msg) }

// Info 记录信息级日志。
func (l *Logger) Info(msg string) { l.log(LevelInfo, msg) }

// Mark 记录标记级日志。
func (l *Logger) Mark(msg string) { l.log(LevelMark, msg) }

// Warn 记录警告级日志。
func (l *Logger) Warn(msg string) { l.log(LevelWarn, msg) }

// Error 记录错误级日志。
func (l *Logger) Error(msg string) { l.log(LevelError, msg) }
