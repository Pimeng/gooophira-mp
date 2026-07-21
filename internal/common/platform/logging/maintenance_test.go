package logging

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeLog(t *testing.T, dir, name string, size int) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// nameForDaysAgo 用相对 now 的天数生成日志文件名（本地日期）。
func nameForDaysAgo(now time.Time, days int) string {
	return now.AddDate(0, 0, -days).Format("2006-01-02") + ".log"
}

func TestMaintenance_CompressesOldLogs(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	active := writeLog(t, dir, now.Format("2006-01-02")+".log", 100) // 今天（活动）
	recent := writeLog(t, dir, nameForDaysAgo(now, 1), 100)          // 1 天前（未达阈值）
	oldName := nameForDaysAgo(now, 10)
	old := writeLog(t, dir, oldName, 500) // 10 天前 → 应压缩

	m := NewMaintenance(dir, func() int { return 3 }, func() int64 { return 0 }, nil)
	m.RunOnce(now)

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("10-day-old .log should be removed after compression")
	}
	if _, err := os.Stat(old + ".gz"); err != nil {
		t.Error("compressed .log.gz should exist")
	}
	if _, err := os.Stat(active); err != nil {
		t.Error("active log must never be compressed")
	}
	if _, err := os.Stat(recent); err != nil {
		t.Error("recent log under threshold should stay plain")
	}
}

func TestMaintenance_CompressDisabled(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old := writeLog(t, dir, nameForDaysAgo(now, 30), 100)
	m := NewMaintenance(dir, func() int { return 0 }, func() int64 { return 0 }, nil) // 0 = 关闭
	m.RunOnce(now)
	if _, err := os.Stat(old); err != nil {
		t.Error("compressAfterDays<=0 should not compress anything")
	}
}

func TestMaintenance_EnforcesSizeCap(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	// 三个历史日志各 1MB + 今天活动 1MB；上限 2MB。
	writeLog(t, dir, nameForDaysAgo(now, 3), 1<<20)
	writeLog(t, dir, nameForDaysAgo(now, 2), 1<<20)
	writeLog(t, dir, nameForDaysAgo(now, 1), 1<<20)
	active := writeLog(t, dir, now.Format("2006-01-02")+".log", 1<<20)

	m := NewMaintenance(dir, func() int { return 0 }, func() int64 { return 2 << 20 }, nil)
	m.RunOnce(now)

	// 活动日志必须保留。
	if _, err := os.Stat(active); err != nil {
		t.Error("active log must never be deleted by size cap")
	}
	// 统计剩余日志总占用应 <= 2MB。
	entries, _ := os.ReadDir(dir)
	var total int64
	for _, e := range entries {
		if info, err := e.Info(); err == nil {
			total += info.Size()
		}
	}
	if total > 2<<20 {
		t.Errorf("total size %d should be capped to <= 2MB", total)
	}
	// 最旧的（3 天前）应最先被删。
	if _, err := os.Stat(filepath.Join(dir, nameForDaysAgo(now, 3))); !os.IsNotExist(err) {
		t.Error("oldest log should be deleted first")
	}
}

func TestMaintenance_GzipRoundtrip(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	content := []byte("hello log line\nanother line\n")
	name := nameForDaysAgo(now, 5)
	if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewMaintenance(dir, func() int { return 1 }, func() int64 { return 0 }, nil)
	m.RunOnce(now)

	f, err := os.Open(filepath.Join(dir, name+".gz"))
	if err != nil {
		t.Fatalf("gz not created: %v", err)
	}
	defer f.Close()
	gr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(gr)
	if string(got) != string(content) {
		t.Errorf("decompressed content mismatch: %q", got)
	}
}

func TestMaintenance_StartStopIdempotent(t *testing.T) {
	dir := t.TempDir()
	m := NewMaintenance(dir, func() int { return 0 }, func() int64 { return 0 }, nil)
	m.Start()
	m.Stop()
	m.Stop() // 二次停止不应阻塞或 panic
}
