package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
	"github.com/Pimeng/gooophira-mp/internal/replay"
	"github.com/Pimeng/gooophira-mp/internal/server"
)

type fakeReplayPhira struct{ id int }

func (f fakeReplayPhira) FetchUserInfo(ctx context.Context, token string) (server.PhiraUserInfo, error) {
	if token == "valid" {
		return server.PhiraUserInfo{ID: f.id, Name: "u"}, nil
	}
	return server.PhiraUserInfo{}, errors.New("auth-failed")
}
func (f fakeReplayPhira) FetchChart(ctx context.Context, id int) (config.Chart, error) {
	return config.Chart{}, nil
}
func (f fakeReplayPhira) FetchRecord(ctx context.Context, id int) (config.RecordData, error) {
	return config.RecordData{}, nil
}

// newReplayService 建一个带回放目录与假 Phira 的服务，并写入用户 100 的一份回放。
func newReplayService(t *testing.T) (*Service, *server.ServerState, string, int64) {
	t.Helper()
	dir := t.TempDir()
	rec := replay.NewRecorder(dir, nil)
	roomID := protocol.RoomID("r")
	rec.StartRoom(roomID, 42, "MyChart", []replay.Participant{{ID: 100, Name: "alice"}})
	rec.AppendTouches(roomID, 100, []protocol.TouchFrame{{Time: 1, Points: []protocol.TouchPoint{{ID: 0, Pos: protocol.CompactPos{X: 0.5, Y: 0.5}}}}})
	rec.SetRecordID(roomID, 100, 777)
	rec.EndRoom(roomID)
	ts := rec.ListRoomFiles(roomID)[0].Timestamp

	cfg := &config.ServerConfig{ReplayBaseDir: &dir}
	state := server.NewServerState(cfg, nil, "test", "", "")
	svc := New(state, server.NewHub(state, fakeReplayPhira{id: 100}), nil)
	return svc, state, dir, ts
}

func doReq(svc *Service, method, path, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	svc.route(w, r)
	return w
}

func TestReplay_AuthListsAndDownloads(t *testing.T) {
	svc, _, _, ts := newReplayService(t)

	// 认证 → 列出回放 + 拿到会话。
	w := doReq(svc, http.MethodPost, "/replay/auth", `{"token":"valid"}`)
	if w.Code != 200 {
		t.Fatalf("auth status = %d body=%s", w.Code, w.Body.String())
	}
	var auth struct {
		OK           bool   `json:"ok"`
		UserID       int    `json:"userId"`
		SessionToken string `json:"sessionToken"`
		Charts       []struct {
			ChartID int `json:"chartId"`
			Replays []struct {
				Timestamp int64 `json:"timestamp"`
				RecordID  int   `json:"recordId"`
			} `json:"replays"`
		} `json:"charts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &auth); err != nil {
		t.Fatal(err)
	}
	if !auth.OK || auth.UserID != 100 || auth.SessionToken == "" {
		t.Fatalf("bad auth response: %s", w.Body.String())
	}
	if len(auth.Charts) != 1 || auth.Charts[0].ChartID != 42 || auth.Charts[0].Replays[0].RecordID != 777 {
		t.Fatalf("unexpected charts: %s", w.Body.String())
	}

	// 下载。
	url := fmt.Sprintf("/replay/download?sessionToken=%s&chartId=42&timestamp=%d", auth.SessionToken, ts)
	w = doReq(svc, http.MethodGet, url, "")
	if w.Code != 200 {
		t.Fatalf("download status = %d body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("content-type"); ct != "application/octet-stream" {
		t.Errorf("content-type = %q", ct)
	}
	if body := w.Body.Bytes(); len(body) < 8 || string(body[0:8]) != "PHIRAREC" {
		t.Errorf("download body is not a PHIRAREC file (len=%d)", len(body))
	}
}

func TestReplay_AuthBadToken(t *testing.T) {
	svc, _, _, _ := newReplayService(t)
	if w := doReq(svc, http.MethodPost, "/replay/auth", `{"token":"nope"}`); w.Code != 401 {
		t.Errorf("invalid token should be 401, got %d", w.Code)
	}
	if w := doReq(svc, http.MethodPost, "/replay/auth", `{}`); w.Code != 400 {
		t.Errorf("missing token should be 400, got %d", w.Code)
	}
}

func TestReplay_DownloadBadSession(t *testing.T) {
	svc, _, _, ts := newReplayService(t)
	url := fmt.Sprintf("/replay/download?sessionToken=bogus&chartId=42&timestamp=%d", ts)
	if w := doReq(svc, http.MethodGet, url, ""); w.Code != 401 {
		t.Errorf("bogus session should be 401, got %d", w.Code)
	}
}

func TestReplay_Delete(t *testing.T) {
	svc, _, dir, ts := newReplayService(t)
	// 先认证拿会话。
	w := doReq(svc, http.MethodPost, "/replay/auth", `{"token":"valid"}`)
	var auth struct {
		SessionToken string `json:"sessionToken"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &auth)

	body := fmt.Sprintf(`{"sessionToken":"%s","chartId":42,"timestamp":%d}`, auth.SessionToken, ts)
	if w := doReq(svc, http.MethodPost, "/replay/delete", body); w.Code != 200 {
		t.Fatalf("delete status = %d body=%s", w.Code, w.Body.String())
	}
	if len(replay.ListReplaysForUser(dir, 100)) != 0 {
		t.Error("replay should be deleted")
	}
}

func TestReplay_UploadToShareStation(t *testing.T) {
	// mock 分享站：/upload_direct 返回 replay_id；/show/ 返回 200。
	ss := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/upload_direct" {
			_ = json.NewEncoder(w).Encode(map[string]any{"replay_id": "100_42_555.phirarec"})
			return
		}
		w.WriteHeader(200)
	}))
	defer ss.Close()

	dir := t.TempDir()
	rec := replay.NewRecorder(dir, nil)
	roomID := protocol.RoomID("r")
	rec.StartRoom(roomID, 42, "MyChart", []replay.Participant{{ID: 100, Name: "alice"}})
	rec.AppendTouches(roomID, 100, []protocol.TouchFrame{{Time: 1, Points: []protocol.TouchPoint{{ID: 0, Pos: protocol.CompactPos{X: 0.5, Y: 0.5}}}}})
	rec.SetRecordID(roomID, 100, 777)
	rec.EndRoom(roomID)
	ts := rec.ListRoomFiles(roomID)[0].Timestamp

	cfg := &config.ServerConfig{ReplayBaseDir: &dir, ShareStation: &config.ShareStation{URL: ss.URL, Token: "tok"}}
	state := server.NewServerState(cfg, nil, "test", "", "")
	svc := New(state, server.NewHub(state, fakeReplayPhira{id: 100}), nil)

	body := fmt.Sprintf(`{"token":"valid","chartId":42,"timestamp":%d}`, ts)
	w := doReq(svc, http.MethodPost, "/replay/upload", body)
	if w.Code != 200 {
		t.Fatalf("upload status = %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		OK      bool `json:"ok"`
		ScoreID int  `json:"scoreId"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.OK || resp.ScoreID != 555 {
		t.Fatalf("unexpected upload response: %s", w.Body.String())
	}
	// 上传成功后本地文件应删除。
	if len(replay.ListReplaysForUser(dir, 100)) != 0 {
		t.Error("local replay should be deleted after upload")
	}
	// 元数据应记录。
	state.Mu.Lock()
	metas := state.UploadedReplayMeta[100][42]
	state.Mu.Unlock()
	if len(metas) != 1 || metas[0].ScoreID != 555 {
		t.Errorf("uploaded meta not stored: %v", metas)
	}
}

func TestReplay_UploadNotConfigured(t *testing.T) {
	svc, _, _, ts := newReplayService(t) // 无 ShareStation
	body := fmt.Sprintf(`{"token":"valid","chartId":42,"timestamp":%d}`, ts)
	if w := doReq(svc, http.MethodPost, "/replay/upload", body); w.Code != http.StatusServiceUnavailable {
		t.Errorf("upload without share station should be 503, got %d", w.Code)
	}
}

func TestReplay_AutoUploadConfig(t *testing.T) {
	svc, state, _, _ := newReplayService(t)

	// GET 默认 show=false。
	w := doReq(svc, http.MethodGet, "/replay/auto-upload/config?token=valid", "")
	if w.Code != 200 {
		t.Fatalf("get config status = %d body=%s", w.Code, w.Body.String())
	}
	var g struct {
		Show bool `json:"show"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &g)
	if g.Show {
		t.Error("show should default to false")
	}

	// POST show=true。
	if w := doReq(svc, http.MethodPost, "/replay/auto-upload/config", `{"token":"valid","show":true}`); w.Code != 200 {
		t.Fatalf("post config status = %d body=%s", w.Code, w.Body.String())
	}
	state.Mu.Lock()
	cfg := state.AutoUploadConfigs[100]
	state.Mu.Unlock()
	if cfg == nil || !cfg.Show {
		t.Error("show should be true after POST")
	}

	// 无 token → 400。
	if w := doReq(svc, http.MethodGet, "/replay/auto-upload/config", ""); w.Code != 400 {
		t.Errorf("missing token should be 400, got %d", w.Code)
	}
}
