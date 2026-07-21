package agentbridge

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/agent/outbox"
	"github.com/Pimeng/gooophira-mp/internal/common/agentproto"
	"github.com/Pimeng/gooophira-mp/internal/common/protocol"
	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/core/server"
)

func TestPublisherMapsServerEventsWithoutServerDTOLeak(t *testing.T) {
	store, err := agentoutbox.Open(agentoutbox.Config{Dir: t.TempDir(), MaxBytes: 2 << 20})
	if err != nil {
		t.Fatal(err)
	}
	publisher := New(store, nil, 8)
	publisher.Emit(server.Event{
		Type: server.EventGameStart, Server: "test", RoomID: "ABCD", ChartID: 7,
		ChartName: "chart", ChartDifficulty: "IN 15", ChartCharter: "author",
		Players: []server.EventPlayer{{ID: 1, Name: "alice"}},
	})
	if err := publisher.PublishCritical("barrier.v1", map[string]bool{"done": true}); err != nil {
		t.Fatal(err)
	}
	publisher.Close()
	defer store.Close()
	events, _, _, err := store.Events(0, 10)
	if err != nil || len(events) != 2 || events[0].Type != agentproto.EventGameStartedV1 || events[1].Type != "barrier.v1" {
		t.Fatalf("ordered events = %+v, %v", events, err)
	}
	var payload agentproto.GameStartedV1
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.RoomID != "ABCD" || payload.Chart.ID != 7 || len(payload.Players) != 1 || payload.Players[0].ID != 1 {
		t.Fatalf("mapped payload = %+v", payload)
	}
}

func TestCaptureMatchFinished(t *testing.T) {
	room := server.NewRoom(protocol.RoomID("ABCD"), 1, 8, true)
	alice := &server.User{ID: 1, Name: "alice"}
	bob := &server.User{ID: 2, Name: "bob"}
	room.AddUser(alice, false)
	room.AddUser(bob, false)
	room.Chart = &config.Chart{ID: 7, Name: "chart", Level: "AT 16", Charter: "author", Illustration: "https://example.test/a.webp"}
	std := 0.012
	stdScore := 1.2
	room.State = server.StatePlaying{
		StartedAt: time.Now().Add(-5 * time.Second),
		Results: map[int]config.RecordData{
			1: {ID: 11, Player: 1, Score: 900000, Accuracy: 0.98, Perfect: 100, Good: 2, MaxCombo: 102, FullCombo: true, Std: &std, StdScore: &stdScore},
			2: {ID: 12, Player: 2, Score: 800000, Accuracy: 0.9, Perfect: 90, Good: 5, Bad: 2, Miss: 3, MaxCombo: 80},
		},
	}
	match, ok := CaptureMatchFinished("test", room)
	if !ok || match.Chart.Charter != "author" || match.DurationSeconds < 4.5 || len(match.Results) != 2 {
		t.Fatalf("match = %+v, ok=%v", match, ok)
	}
	if match.Results[0].Player.Name != "alice" || match.Results[0].Rank != 1 || match.Results[0].Std == nil || match.Results[1].Rank != 2 || match.Results[1].Std != nil {
		t.Fatalf("results = %+v", match.Results)
	}
	AttachReplayIDs(&match, map[int]string{1: "v1:1:7:123"})
	if match.Results[0].ReplayID == "" || match.Results[1].ReplayID != "" {
		t.Fatalf("replay IDs = %+v", match.Results)
	}
}
