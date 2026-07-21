package load

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestFileWatcher_FiresOnChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server_config.yml")
	if err := os.WriteFile(path, []byte("REPLAY_ENABLED: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fired := make(chan struct{}, 4)
	w := NewFileWatcher(path, 10*time.Millisecond, func() { fired <- struct{}{} })
	w.Start()
	defer w.Stop()

	// 修改文件（确保 mtime 推进）。
	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(path, []byte("REPLAY_ENABLED: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case <-fired:
		// 变更通知已收到。
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not fire on file change")
	}
}

func TestFileWatcher_NoSpuriousFireAtStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server_config.yml")
	if err := os.WriteFile(path, []byte("X: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var count int32
	w := NewFileWatcher(path, 10*time.Millisecond, func() { atomic.AddInt32(&count, 1) })
	w.Start()
	time.Sleep(60 * time.Millisecond) // 多个轮询周期，文件不变
	w.Stop()
	if c := atomic.LoadInt32(&count); c != 0 {
		t.Errorf("unchanged file should not fire, got %d", c)
	}
}

func TestFileWatcher_StopIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.yml")
	_ = os.WriteFile(path, []byte("X: 1\n"), 0o644)
	w := NewFileWatcher(path, 10*time.Millisecond, func() {})
	w.Start()
	w.Stop()
	w.Stop() // 第二次不应阻塞或 panic
}

func TestConfigDirWatcher_FiresWhenOptionalFileAppears(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, CoreConfigFile), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fired := make(chan struct{}, 1)
	w := NewConfigDirWatcher(dir, 10*time.Millisecond, func() { fired <- struct{}{} })
	w.Start()
	defer w.Stop()

	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "replay.yaml"), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("directory watcher did not detect optional file")
	}
}
