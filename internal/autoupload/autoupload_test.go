package autoupload

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
	"github.com/Pimeng/gooophira-mp/internal/replay"
	"github.com/Pimeng/gooophira-mp/internal/server"
)

// setup 建一个带回放文件 + 配置好分享站的 state，返回 (uploader, state, baseDir, timestamp, visibilityCalls)。
func setup(t *testing.T, autoUpload bool) (*Uploader, *server.ServerState, string, int64, *int32, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	rec := replay.NewRecorder(dir, nil)
	roomID := protocol.RoomID("r")
	rec.StartRoom(roomID, 42, "MyChart", []replay.Participant{{ID: 100, Name: "alice"}})
	rec.AppendTouches(roomID, 100, []protocol.TouchFrame{{Time: 1, Points: []protocol.TouchPoint{{ID: 0, Pos: protocol.CompactPos{X: 0.5, Y: 0.5}}}}})
	rec.SetRecordID(roomID, 100, 777)
	rec.EndRoom(roomID)
	ts := rec.ListRoomFiles(roomID)[0].Timestamp

	var visCalls int32
	ss := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/upload_direct":
			_ = json.NewEncoder(w).Encode(map[string]any{"replay_id": "100_42_555.phirarec"})
		default: // /show/ or /hide/
			atomic.AddInt32(&visCalls, 1)
			w.WriteHeader(200)
		}
	}))

	au := autoUpload
	cfg := &config.ServerConfig{
		ReplayBaseDir:    &dir,
		ReplayAutoUpload: &au,
		ShareStation:     &config.ShareStation{URL: ss.URL, Token: "tok"},
	}
	state := server.NewServerState(cfg, nil, "test", "", "")
	return New(state, time.Millisecond), state, dir, ts, &visCalls, ss
}

func TestAutoUpload_UploadsAndDeletes(t *testing.T) {
	u, state, dir, ts, _, ss := setup(t, true)
	defer ss.Close()

	u.uploadNow(100, 42, ts) // 直接调用同步上传逻辑

	if len(replay.ListReplaysForUser(dir, 100)) != 0 {
		t.Error("local replay should be deleted after auto upload")
	}
	state.Mu.Lock()
	metas := state.UploadedReplayMeta[100][42]
	state.Mu.Unlock()
	if len(metas) != 1 || metas[0].ScoreID != 555 {
		t.Errorf("uploaded meta not stored: %v", metas)
	}
}

func TestAutoUpload_VisibilityFollowsUserConfig(t *testing.T) {
	u, state, _, ts, visCalls, ss := setup(t, true)
	defer ss.Close()

	// 用户未开启 show → 不应调用 /show。
	u.uploadNow(100, 42, ts)
	if atomic.LoadInt32(visCalls) != 0 {
		t.Errorf("visibility should not be set when user show=false, got %d calls", *visCalls)
	}

	// 重建文件 + 用户开启 show → 应调用一次可见性。
	_, state2, _, ts2, visCalls2, ss2 := setup(t, true)
	defer ss2.Close()
	state2.AutoUploadConfigs[100] = &server.AutoUploadConfig{Show: true}
	u2 := New(state2, time.Millisecond)
	u2.uploadNow(100, 42, ts2)
	if atomic.LoadInt32(visCalls2) != 1 {
		t.Errorf("visibility should be set once when user show=true, got %d", *visCalls2)
	}
	_ = state
}

func TestAutoUpload_SkipsWhenDisabled(t *testing.T) {
	u, _, dir, ts, _, ss := setup(t, false) // REPLAY_AUTO_UPLOAD off
	defer ss.Close()

	u.Handle(100, 42, ts, 777) // 应直接跳过（不调度）
	u.mu.Lock()
	n := len(u.timers)
	u.mu.Unlock()
	if n != 0 {
		t.Errorf("disabled auto-upload should not schedule, got %d timers", n)
	}
	// 文件应保留。
	if len(replay.ListReplaysForUser(dir, 100)) != 1 {
		t.Error("local replay should be kept when auto-upload disabled")
	}
}

func TestAutoUpload_HandleSchedulesAndCloses(t *testing.T) {
	u, state, dir, ts, _, ss := setup(t, true)
	defer ss.Close()

	u.Handle(100, 42, ts, 777) // 1ms 后触发上传
	// 等待上传完成（文件被删 + meta 写入）。
	deadline := time.After(2 * time.Second)
	for {
		state.Mu.Lock()
		done := len(state.UploadedReplayMeta[100]) > 0
		state.Mu.Unlock()
		if done {
			break
		}
		select {
		case <-deadline:
			t.Fatal("auto upload did not complete after Handle")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if len(replay.ListReplaysForUser(dir, 100)) != 0 {
		t.Error("file should be deleted after scheduled upload")
	}

	u.Close()
	u.Close() // 幂等
}
