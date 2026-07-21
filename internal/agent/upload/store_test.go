package agentupload

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/agent/integration/sharestation"
	"github.com/Pimeng/gooophira-mp/internal/common/protocol"
	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/core/replay"
)

type fakeClient struct {
	uploads int
	visible []int
}

func (f *fakeClient) Upload(_ []byte, _, _, _ string) (sharestation.UploadResult, error) {
	f.uploads++
	return sharestation.UploadResult{ReplayID: "100_42_555.phirarec", ScoreID: 555}, nil
}

func (f *fakeClient) SetVisibility(id int, visible bool) error {
	if visible {
		f.visible = append(f.visible, id)
	}
	return nil
}

func TestUploadIsPersistentAndIdempotent(t *testing.T) {
	base := t.TempDir()
	rec := replay.NewRecorder(base, nil)
	roomID := protocol.RoomID("r")
	rec.StartRoom(roomID, 42, "chart", []replay.Participant{{ID: 100, Name: "alice"}})
	rec.SetRecordID(roomID, 100, 777)
	rec.EndRoom(roomID)
	file := rec.ListRoomFiles(roomID)[0]
	id := replay.IDFromFile(file).String()
	cfg := config.AgentReplayUploadConfig{Enabled: true, BaseDir: base, StatePath: filepath.Join(t.TempDir(), "state.json"), DelayMS: 1}
	client := &fakeClient{}
	store, err := openWithClient(cfg, client)
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.Upload(context.Background(), id, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.ScoreID != 555 || result.RecordID != 777 || client.uploads != 1 || len(client.visible) != 1 {
		t.Fatalf("result=%+v client=%+v", result, client)
	}
	if _, err := os.Stat(file.Path); !os.IsNotExist(err) {
		t.Fatalf("replay should be deleted, stat err=%v", err)
	}
	store2, err := openWithClient(cfg, client)
	if err != nil {
		t.Fatal(err)
	}
	result, err = store2.Upload(context.Background(), id, true)
	if err != nil || result.ScoreID != 555 || client.uploads != 1 || len(client.visible) != 2 {
		t.Fatalf("idempotent result=%+v err=%v uploads=%d", result, err, client.uploads)
	}
}

func TestAutoConfigPersists(t *testing.T) {
	cfg := config.AgentReplayUploadConfig{StatePath: filepath.Join(t.TempDir(), "state.json")}
	client := &fakeClient{}
	store, err := openWithClient(cfg, client)
	if err != nil {
		t.Fatal(err)
	}
	show := true
	if got, err := store.AutoConfig(100, &show); err != nil || !got {
		t.Fatalf("set show=%v err=%v", got, err)
	}
	store, err = openWithClient(cfg, client)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := store.AutoConfig(100, nil); err != nil || !got {
		t.Fatalf("persisted show=%v err=%v", got, err)
	}
}
