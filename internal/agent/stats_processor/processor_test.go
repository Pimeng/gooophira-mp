package agentstats

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/agent/inbox"
	"github.com/Pimeng/gooophira-mp/internal/agent/stats_store"
	"github.com/Pimeng/gooophira-mp/internal/common/agentproto"
)

func matchEnvelope(t *testing.T, sequence uint64, id string) agentproto.Envelope {
	t.Helper()
	payload := agentproto.MatchFinishedV1{
		RoomID: "R", Chart: agentproto.ChartV1{ID: 42, Name: "chart"}, DurationSeconds: 60,
		Results: []agentproto.MatchPlayerResultV1{{Player: agentproto.PlayerV1{ID: 1, Name: "alice"}, Score: 900000, Accuracy: .98, Rank: 1}},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return agentproto.Envelope{Version: 1, ID: id, Sequence: sequence, Type: agentproto.EventMatchFinishedV1, Payload: data}
}

func TestStatsProcessorTransactionalIdempotency(t *testing.T) {
	root := t.TempDir()
	inbox, err := agentinbox.Open(filepath.Join(root, "events.log"), 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer inbox.Close()
	if _, err := inbox.Accept([]agentproto.Envelope{matchEnvelope(t, 1, "match-event")}); err != nil {
		t.Fatal(err)
	}
	store, err := stats.Open(filepath.Join(root, "stats.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cursor := filepath.Join(root, "cursor")
	processor, err := OpenProcessor(inbox, store, cursor)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := processor.Process(context.Background(), 10); err != nil || n != 1 {
		t.Fatalf("first process n=%d err=%v", n, err)
	}
	profile, err := store.GetPlayerProfile(1)
	if err != nil || profile.Games != 1 {
		t.Fatalf("profile after first process = %+v err=%v", profile, err)
	}
	// 模拟保留已提交数据库但丢失处理游标的情况。
	if err := writeCursor(cursor, 0); err != nil {
		t.Fatal(err)
	}
	processor, err = OpenProcessor(inbox, store, cursor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := processor.Process(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	profile, err = store.GetPlayerProfile(1)
	if err != nil || profile.Games != 1 {
		t.Fatalf("duplicate event changed stats: %+v err=%v", profile, err)
	}
}

func TestQueryHandlerPreservesStatsJSON(t *testing.T) {
	store, err := stats.Open(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler := QueryHandler{Store: store}
	response := handler.Handle(agentproto.QueryRequest{ID: "q", Method: agentproto.QueryStatsPlayer, Params: []byte(`{"id":999}`)})
	if response.StatusCode != 404 {
		t.Fatalf("missing player status = %d body=%s", response.StatusCode, response.Body)
	}
}
