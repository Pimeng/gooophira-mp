package agentwebhook

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/agent/inbox"
	"github.com/Pimeng/gooophira-mp/internal/agent/integration/webhook"
	"github.com/Pimeng/gooophira-mp/internal/common/agentproto"
	"github.com/Pimeng/gooophira-mp/internal/common/webhookmodel"
	"github.com/Pimeng/gooophira-mp/internal/config"
)

type fakeDeliverer struct {
	fail   bool
	events []webhookmodel.Event
}

type fakePlanner struct {
	failB bool
	calls map[string]int
}

func (p *fakePlanner) Plan(webhookmodel.Event) []webhook.TargetDelivery {
	return []webhook.TargetDelivery{{ID: "a", Target: config.WebhookTarget{URL: "a"}}, {ID: "b", Target: config.WebhookTarget{URL: "b"}}}
}

func (p *fakePlanner) DeliverTarget(_ context.Context, target config.WebhookTarget, _ webhookmodel.Event) (webhook.DeliveryOutcome, error) {
	p.calls[target.URL]++
	if target.URL == "b" && p.failB {
		return webhook.DeliveryRetryableFailure, errors.New("retry b")
	}
	return webhook.DeliverySucceeded, nil
}

func (d *fakeDeliverer) DeliverEvent(_ context.Context, event webhookmodel.Event) error {
	if d.fail {
		return errors.New("temporary failure")
	}
	d.events = append(d.events, event)
	return nil
}

func agentEnvelope(t *testing.T, sequence uint64, typ string, payload any) agentproto.Envelope {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return agentproto.Envelope{Version: 1, ID: string(rune('a' + sequence)), Sequence: sequence, Type: typ, Payload: data}
}

func TestProcessorRetriesWithoutAdvancingAndResumes(t *testing.T) {
	root := t.TempDir()
	inbox, err := agentinbox.Open(filepath.Join(root, "events.log"), 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer inbox.Close()
	match := agentproto.MatchFinishedV1{Server: "test", RoomID: "R", Results: []agentproto.MatchPlayerResultV1{{Player: agentproto.PlayerV1{ID: 1, Name: "alice"}, Score: 900000, Rank: 1}}}
	if _, err := inbox.Accept([]agentproto.Envelope{agentEnvelope(t, 1, agentproto.EventMatchFinishedV1, match)}); err != nil {
		t.Fatal(err)
	}
	deliverer := &fakeDeliverer{fail: true}
	cursorPath := filepath.Join(root, "cursor")
	processor, err := OpenProcessor(inbox, deliverer, nil, nil, cursorPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := processor.Process(context.Background(), 10); err == nil || processor.Cursor() != 0 {
		t.Fatalf("failed delivery advanced cursor: cursor=%d err=%v", processor.Cursor(), err)
	}
	deliverer.fail = false
	if n, err := processor.Process(context.Background(), 10); err != nil || n != 1 || processor.Cursor() != 1 {
		t.Fatalf("successful retry n=%d cursor=%d err=%v", n, processor.Cursor(), err)
	}
	processor, err = OpenProcessor(inbox, deliverer, nil, nil, cursorPath)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := processor.Process(context.Background(), 10); err != nil || n != 0 || len(deliverer.events) != 1 {
		t.Fatalf("restart redelivered completed event: n=%d deliveries=%d err=%v", n, len(deliverer.events), err)
	}
}

func TestLedgerPersistsCompletedTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger")
	ledger, err := OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.Mark("event", "target-a", ledgerSucceeded); err != nil {
		t.Fatal(err)
	}
	ledger.Close()
	ledger, err = OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	if !ledger.Done("event", "target-a") || ledger.Done("event", "target-b") {
		t.Fatal("ledger did not recover per-target terminal state")
	}
}

func TestProcessorDoesNotRedeliverCompletedTargetAfterRestart(t *testing.T) {
	root := t.TempDir()
	inbox, err := agentinbox.Open(filepath.Join(root, "events.log"), 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer inbox.Close()
	event := agentEnvelope(t, 1, agentproto.EventRoomDisbandedV1, agentproto.RoomEventV1{RoomID: "R"})
	if _, err := inbox.Accept([]agentproto.Envelope{event}); err != nil {
		t.Fatal(err)
	}
	ledgerPath := filepath.Join(root, "ledger")
	ledger, err := OpenLedger(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	planner := &fakePlanner{failB: true, calls: make(map[string]int)}
	processor, err := OpenProcessor(inbox, &fakeDeliverer{}, planner, ledger, filepath.Join(root, "cursor"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := processor.Process(context.Background(), 10); err == nil {
		t.Fatal("target b failure should stop processing")
	}
	if planner.calls["a"] != 1 || planner.calls["b"] != 1 {
		t.Fatalf("first delivery calls = %+v", planner.calls)
	}
	ledger.Close()
	ledger, err = OpenLedger(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	planner.failB = false
	processor, err = OpenProcessor(inbox, &fakeDeliverer{}, planner, ledger, filepath.Join(root, "cursor"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := processor.Process(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if planner.calls["a"] != 1 || planner.calls["b"] != 2 {
		t.Fatalf("restart delivery calls = %+v", planner.calls)
	}
}

func TestConvertDomainEvents(t *testing.T) {
	started, deliver, err := Convert(agentEnvelope(t, 1, agentproto.EventGameStartedV1, agentproto.GameStartedV1{
		Server: "test", RoomID: "R", Chart: agentproto.ChartV1{ID: 2, Name: "chart"}, Players: []agentproto.PlayerV1{{ID: 1, Name: "alice"}},
	}))
	if err != nil || !deliver || started.Type != webhookmodel.EventGameStart || started.UserCount != 1 || started.PlayerList != "alice(1)" {
		t.Fatalf("game started conversion = %+v deliver=%v err=%v", started, deliver, err)
	}
	replay, deliver, err := Convert(agentEnvelope(t, 2, agentproto.EventReplayCompletedV1, agentproto.ReplayCompletedV1{}))
	if err != nil || deliver || replay.Type != "" {
		t.Fatalf("replay event should not produce webhook: %+v deliver=%v err=%v", replay, deliver, err)
	}
}
