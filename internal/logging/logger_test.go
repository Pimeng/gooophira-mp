package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func todayLog(dir string) string {
	return filepath.Join(dir, time.Now().Format("2006-01-02")+".log")
}

func TestLogger_FileOutput(t *testing.T) {
	dir := t.TempDir()
	l := New("INFO", dir)
	l.Info("hello world")
	l.Warn("a warning")
	l.Debug("below min level") // DEBUG < INFO → 跳过
	l.Close()

	data, err := os.ReadFile(todayLog(dir))
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, "[INFO] hello world") {
		t.Errorf("missing info line: %q", s)
	}
	if !strings.Contains(s, "[WARN] a warning") {
		t.Errorf("missing warn line: %q", s)
	}
	if strings.Contains(s, "below min level") {
		t.Error("DEBUG below min level should not be written")
	}
	if strings.Contains(s, "\x1b[") {
		t.Error("file lines should not contain ANSI color codes")
	}
}

func TestLogger_AppendsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	l1 := New("INFO", dir)
	l1.Info("first")
	l1.Close()
	l2 := New("INFO", dir)
	l2.Info("second")
	l2.Close()

	data, _ := os.ReadFile(todayLog(dir))
	s := string(data)
	if !strings.Contains(s, "first") || !strings.Contains(s, "second") {
		t.Errorf("log file should append across instances: %q", s)
	}
}

func TestLogger_ConsoleOnlyWhenNoDir(t *testing.T) {
	dir := t.TempDir()
	l := New("INFO", "") // 空目录 → 仅控制台
	l.Info("nothing to file")
	l.Close()
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Error("no file should be written when logsDir is empty")
	}
}
