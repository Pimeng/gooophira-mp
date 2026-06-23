package replay

import (
	"os"
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

func TestRecorder_WriteAndDecode(t *testing.T) {
	dir := t.TempDir()
	rec := NewRecorder(dir, nil)
	roomID := protocol.RoomID("room1")

	rec.StartRoom(roomID, 42, "MyChart", []Participant{{ID: 100, Name: "alice"}})
	rec.AppendTouches(roomID, 100, []protocol.TouchFrame{
		{Time: 1.5, Points: []protocol.TouchPoint{{ID: 0, Pos: protocol.CompactPos{X: 0.5, Y: -0.5}}}},
	})
	rec.AppendJudges(roomID, 100, []protocol.JudgeEvent{
		{Time: 2.0, LineID: 3, NoteID: 4, Judgement: protocol.JudgeGood},
	})
	rec.SetRecordID(roomID, 100, 777)
	rec.EndRoom(roomID) // 测试中同步执行

	files := rec.ListRoomFiles(roomID)
	if len(files) != 1 {
		t.Fatalf("expected 1 completed file, got %d", len(files))
	}

	raw, err := os.ReadFile(files[0].Path)
	if err != nil {
		t.Fatalf("read replay file: %v", err)
	}
	if !isPhiraRecordV2(raw) {
		t.Fatal("file is not a valid PHIRAREC v2 record")
	}
	if raw[12] != compressionDeflate {
		t.Fatalf("compression byte = %d, want DEFLATE(%d)", raw[12], compressionDeflate)
	}

	content, err := decodePayload(raw)
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	r := protocol.NewBinaryReader(content)
	recordID := r.ReadI32()
	_ = r.ReadI64() // timestamp
	chartID := r.ReadI32()
	chartName := r.ReadString()
	userID := r.ReadI32()
	userName := r.ReadString()
	touches := protocol.ReadArray(r, protocol.DecodeTouchFrame)
	judges := protocol.ReadArray(r, decodeReplayJudgeEvent)

	if recordID != 777 {
		t.Errorf("recordID = %d, want 777", recordID)
	}
	if chartID != 42 || chartName != "MyChart" {
		t.Errorf("chart = (%d, %q), want (42, MyChart)", chartID, chartName)
	}
	if userID != 100 || userName != "alice" {
		t.Errorf("user = (%d, %q), want (100, alice)", userID, userName)
	}
	if len(touches) != 1 || touches[0].Time != 1.5 || touches[0].Points[0].Pos.X != 0.5 {
		t.Errorf("touches roundtrip mismatch: %+v", touches)
	}
	if len(judges) != 1 || judges[0].LineID != 3 || judges[0].NoteID != 4 || judges[0].Judgement != protocol.JudgeGood {
		t.Errorf("judges roundtrip mismatch: %+v", judges)
	}
}

func TestRecorder_StartRoomSkipsIfAlreadyRecording(t *testing.T) {
	rec := NewRecorder(t.TempDir(), nil)
	roomID := protocol.RoomID("room1")
	rec.StartRoom(roomID, 1, "a", []Participant{{ID: 1, Name: "u1"}})
	// 第二次 StartRoom（不同谱面）应被跳过，原录制保留。
	rec.StartRoom(roomID, 999, "b", []Participant{{ID: 1, Name: "u1"}})
	rec.SetRecordID(roomID, 1, 5)
	rec.EndRoom(roomID)
	files := rec.ListRoomFiles(roomID)
	if len(files) != 1 || files[0].ChartID != 1 {
		t.Fatalf("second StartRoom should be skipped, files=%+v", files)
	}
}

func TestRecorder_AppendToUnknownRoomIsNoop(t *testing.T) {
	rec := NewRecorder(t.TempDir(), nil)
	// 未 StartRoom 直接 append，不应 panic。
	rec.AppendTouches("nope", 1, []protocol.TouchFrame{{Time: 1}})
	rec.AppendJudges("nope", 1, []protocol.JudgeEvent{{Time: 1}})
	rec.SetRecordID("nope", 1, 1)
	if files := rec.ListRoomFiles("nope"); len(files) != 0 {
		t.Fatalf("unknown room should have no files, got %d", len(files))
	}
}

func TestRecorder_OverflowCapsFrames(t *testing.T) {
	rec := NewRecorder(t.TempDir(), nil)
	roomID := protocol.RoomID("room1")
	rec.StartRoom(roomID, 1, "a", []Participant{{ID: 1}})
	// 追加超过上限的触摸帧，应被钳到 maxFramesPerInflight。
	big := make([]protocol.TouchFrame, maxFramesPerInflight+500)
	rec.AppendTouches(roomID, 1, big)
	rec.AppendTouches(roomID, 1, big) // 再追加应全部丢弃
	rec.mu.Lock()
	it := rec.inflight[key(string(roomID), 1)]
	got := len(it.touches)
	rec.mu.Unlock()
	if got != maxFramesPerInflight {
		t.Fatalf("touch frames should cap at %d, got %d", maxFramesPerInflight, got)
	}
}
