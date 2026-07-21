package replay

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func mkReplayFile(t *testing.T, base string, userID, chartID int, ts int64) string {
	t.Helper()
	dir := filepath.Join(base, strconv.Itoa(userID), strconv.Itoa(chartID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, fmt.Sprintf("%d.phirarec", ts))
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCleanupExpired(t *testing.T) {
	base := t.TempDir()
	now := time.Now()
	oldFile := mkReplayFile(t, base, 100, 1, now.Add(-5*24*time.Hour).UnixMilli()) // 5 天前
	newFile := mkReplayFile(t, base, 100, 1, now.UnixMilli())                      // 今天
	staleUserFile := mkReplayFile(t, base, 200, 7, now.Add(-10*24*time.Hour).UnixMilli())

	CleanupExpired(base, now, 4) // TTL 4 天

	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Error("5-day-old file should be deleted under TTL=4")
	}
	if _, err := os.Stat(newFile); err != nil {
		t.Error("today's file should be kept")
	}
	if _, err := os.Stat(staleUserFile); !os.IsNotExist(err) {
		t.Error("10-day-old file should be deleted")
	}
	// user 200 的所有文件都过期 → 其目录应被清空移除。
	if _, err := os.Stat(filepath.Join(base, "200")); !os.IsNotExist(err) {
		t.Error("empty user dir should be removed")
	}
	// user 100 仍有今天的文件 → 目录保留。
	if _, err := os.Stat(filepath.Join(base, "100", "1")); err != nil {
		t.Error("non-empty chart dir should be kept")
	}
}

func TestCleanupExpired_TTLZeroNoop(t *testing.T) {
	base := t.TempDir()
	f := mkReplayFile(t, base, 1, 1, time.Now().Add(-100*24*time.Hour).UnixMilli())
	CleanupExpired(base, time.Now(), 0) // ttl<=0 → 不清理
	if _, err := os.Stat(f); err != nil {
		t.Error("ttl=0 should not delete anything")
	}
}
