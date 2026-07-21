package logging

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// 日志维护：周期压缩历史日志 + 控制日志目录总占用。对应 TS utils/logMaintenance.ts。
//
// 两条独立策略，均按 getter 读取以支持配置热重载：
//  1. 压缩：历史日志超过 compressAfterDays 天后 gzip 化（日志重复率高，压缩可大幅降低占用）；
//     <=0 关闭。最近日志保持明文便于直接查看。
//  2. 容量控制：目录总占用超过 maxTotalBytes 时，从最旧开始删除直到回落上限以下；<=0 不限制。
//
// 当天正在写入的日志（active）永不压缩、永不删除。

// 仅识别 logger 产出的按日切分文件：2026-06-09.log / 2026-06-09.log.gz。
var (
	plainLogRe = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})\.log$`)
	anyLogRe   = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})\.log(?:\.gz)?$`)
)

// Maintenance 是日志维护任务（每日午夜执行 + 启动时一次）。
type Maintenance struct {
	logsDir           string
	getCompressAfterD func() int
	getMaxTotalBytes  func() int64
	logger            *Logger
	stop              chan struct{}
	done              chan struct{}
}

// NewMaintenance 创建日志维护任务（尚未启动）。getter 在每次执行时读取，支持热重载。
// logger 可为 nil（不记录维护日志）。
func NewMaintenance(logsDir string, getCompressAfterDays func() int, getMaxTotalBytes func() int64, logger *Logger) *Maintenance {
	return &Maintenance{
		logsDir:           logsDir,
		getCompressAfterD: getCompressAfterDays,
		getMaxTotalBytes:  getMaxTotalBytes,
		logger:            logger,
		stop:              make(chan struct{}),
		done:              make(chan struct{}),
	}
}

// Start 启动后台任务：先立即跑一次，之后每日午夜执行。
func (m *Maintenance) Start() {
	go func() {
		defer close(m.done)
		m.RunOnce(time.Now())
		for {
			timer := time.NewTimer(durUntilNextMidnight(time.Now()))
			select {
			case <-m.stop:
				timer.Stop()
				return
			case <-timer.C:
				m.RunOnce(time.Now())
			}
		}
	}()
}

// Stop 停止任务并等待退出（幂等）。
func (m *Maintenance) Stop() {
	select {
	case <-m.stop:
	default:
		close(m.stop)
	}
	<-m.done
}

// RunOnce 执行一次维护：压缩过期日志 + 控制总占用。now 注入便于测试。
func (m *Maintenance) RunOnce(now time.Time) {
	entries, err := os.ReadDir(m.logsDir)
	if err != nil {
		return // 目录不存在等，静默
	}
	activeName := now.Format("2006-01-02") + ".log"
	m.compressOldLogs(entries, activeName, now)
	m.enforceSizeCap(activeName)
}

func (m *Maintenance) compressOldLogs(entries []os.DirEntry, activeName string, now time.Time) {
	days := 0
	if m.getCompressAfterD != nil {
		days = m.getCompressAfterD()
	}
	if days <= 0 {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if name == activeName || !plainLogRe.MatchString(name) { // 仅压缩未压缩的非活动 .log
			continue
		}
		dt, ok := parseLogDate(name)
		if !ok {
			continue
		}
		if now.Sub(dt).Hours()/24 < float64(days) {
			continue
		}
		src := filepath.Join(m.logsDir, name)
		dst := src + ".gz"
		if err := gzipFile(src, dst); err != nil {
			m.warn("Log compress failed for " + name + ": " + err.Error())
			_ = os.Remove(dst) // 清理半成品，避免下次重复
			continue
		}
		_ = os.Remove(src)
		m.debug("Log compressed: " + name + " -> " + name + ".gz")
	}
}

func (m *Maintenance) enforceSizeCap(activeName string) {
	var maxBytes int64
	if m.getMaxTotalBytes != nil {
		maxBytes = m.getMaxTotalBytes()
	}
	if maxBytes <= 0 {
		return
	}
	entries, err := os.ReadDir(m.logsDir)
	if err != nil {
		return
	}
	type logFile struct {
		name string
		size int64
		dt   time.Time
	}
	var files []logFile
	var total int64
	for _, e := range entries {
		name := e.Name()
		if !anyLogRe.MatchString(name) {
			continue
		}
		dt, ok := parseLogDate(name)
		if !ok {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // 刚被移动/删除
		}
		files = append(files, logFile{name: name, size: info.Size(), dt: dt})
		total += info.Size()
	}
	if total <= maxBytes {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].dt.Before(files[j].dt) }) // 旧→新
	for _, f := range files {
		if total <= maxBytes {
			break
		}
		if f.name == activeName { // 绝不删除正在写入的日志
			continue
		}
		if err := os.Remove(filepath.Join(m.logsDir, f.name)); err != nil {
			m.warn("Log remove failed for " + f.name + ": " + err.Error())
			continue
		}
		total -= f.size
		m.mark("Log removed to enforce size cap: " + f.name)
	}
}

// gzipFile 把 src 压缩为 dst（gzip）。
func gzipFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	gz := gzip.NewWriter(out)
	if _, err = io.Copy(gz, in); err != nil {
		_ = gz.Close()
		return err
	}
	return gz.Close()
}

// parseLogDate 从日志文件名解析当天 0 点（本地时区）。无法解析返回 ok=false。
func parseLogDate(name string) (time.Time, bool) {
	m := anyLogRe.FindStringSubmatch(name)
	if m == nil {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("2006-01-02", m[1]+"-"+m[2]+"-"+m[3], time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func durUntilNextMidnight(now time.Time) time.Duration {
	y, mo, d := now.Date()
	next := time.Date(y, mo, d+1, 0, 0, 0, 0, now.Location())
	return next.Sub(now)
}

func (m *Maintenance) debug(msg string) {
	if m.logger != nil {
		m.logger.Debug(msg)
	}
}
func (m *Maintenance) warn(msg string) {
	if m.logger != nil {
		m.logger.Warn(msg)
	}
}
func (m *Maintenance) mark(msg string) {
	if m.logger != nil {
		m.logger.Mark(msg)
	}
}
