package replay

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

// writeReplayViaRecorder 用录制器产出一份真实回放文件，返回其 timestamp。
func writeReplayViaRecorder(t *testing.T, dir string, userID, chartID, recordID int, chartName, userName string) int64 {
	t.Helper()
	rec := NewRecorder(dir, nil)
	roomID := protocol.RoomID("r")
	rec.StartRoom(roomID, chartID, chartName, []Participant{{ID: userID, Name: userName}})
	rec.AppendTouches(roomID, userID, []protocol.TouchFrame{
		{Time: 1, Points: []protocol.TouchPoint{{ID: 0, Pos: protocol.CompactPos{X: 0.5, Y: 0.5}}}},
	})
	rec.SetRecordID(roomID, userID, recordID)
	rec.EndRoom(roomID)
	files := rec.ListRoomFiles(roomID)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	return files[0].Timestamp
}

func TestReadReplayHeader(t *testing.T) {
	dir := t.TempDir()
	ts := writeReplayViaRecorder(t, dir, 100, 42, 777, "MyChart", "alice")

	h, err := ReadReplayHeader(FilePath(dir, 100, 42, ts))
	if err != nil {
		t.Fatalf("read header: %v", err)
	}
	if h.UserID != 100 || h.ChartID != 42 || h.RecordID != 777 {
		t.Errorf("header ids = user %d chart %d record %d; want 100/42/777", h.UserID, h.ChartID, h.RecordID)
	}
	if h.ChartName != "MyChart" || h.UserName != "alice" {
		t.Errorf("header names = %q/%q; want MyChart/alice", h.ChartName, h.UserName)
	}
	if h.Timestamp != ts {
		t.Errorf("header ts = %d, want %d", h.Timestamp, ts)
	}
}

func TestReadReplayHeader_BadFile(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.phirarec")
	_ = os.WriteFile(bad, []byte("not a replay"), 0o644)
	if _, err := ReadReplayHeader(bad); err == nil {
		t.Error("expected error for non-PHIRAREC file")
	}
	if _, err := ReadReplayHeader(filepath.Join(dir, "missing.phirarec")); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestListReplaysForUser(t *testing.T) {
	dir := t.TempDir()
	writeReplayViaRecorder(t, dir, 100, 42, 1, "C1", "alice")
	writeReplayViaRecorder(t, dir, 100, 7, 2, "C2", "alice")
	writeReplayViaRecorder(t, dir, 200, 42, 3, "C1", "bob") // 另一个用户，不应混入

	list := ListReplaysForUser(dir, 100)
	if len(list) != 2 {
		t.Fatalf("user 100 should have replays in 2 charts, got %d", len(list))
	}
	if len(list[42]) != 1 || list[42][0].RecordID != 1 {
		t.Errorf("chart 42 entry wrong: %v", list[42])
	}
	if len(list[7]) != 1 || list[7][0].RecordID != 2 {
		t.Errorf("chart 7 entry wrong: %v", list[7])
	}

	// 不存在的用户 → 空 map。
	if len(ListReplaysForUser(dir, 999)) != 0 {
		t.Error("missing user should yield empty list")
	}
}

func TestDeleteReplayForUser(t *testing.T) {
	dir := t.TempDir()
	ts := writeReplayViaRecorder(t, dir, 100, 42, 1, "C1", "alice")

	ok, err := DeleteReplayForUser(dir, 100, 42, ts)
	if err != nil || !ok {
		t.Fatalf("delete should succeed: ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(FilePath(dir, 100, 42, ts)); !os.IsNotExist(err) {
		t.Error("file should be gone after delete")
	}
	// 空目录应被清理。
	if _, err := os.Stat(filepath.Join(dir, "100")); !os.IsNotExist(err) {
		t.Error("empty user dir should be removed")
	}
	// 再次删除 → (false, nil)。
	if ok, err := DeleteReplayForUser(dir, 100, 42, ts); ok || err != nil {
		t.Errorf("deleting missing file should be (false, nil), got (%v, %v)", ok, err)
	}
}
