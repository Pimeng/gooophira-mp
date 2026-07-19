package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/agentinbox"
	"github.com/Pimeng/gooophira-mp/internal/agentipc"
	"github.com/Pimeng/gooophira-mp/internal/agentoutbox"
	"github.com/Pimeng/gooophira-mp/internal/agentproto"
	"github.com/Pimeng/gooophira-mp/internal/agentwebhook"
	"github.com/Pimeng/gooophira-mp/internal/webhookmodel"
)

type failingWebhookDeliverer struct{ fail bool }

func (d *failingWebhookDeliverer) DeliverEvent(context.Context, webhookmodel.Event) error {
	if d.fail {
		return errors.New("webhook unavailable")
	}
	return nil
}

func TestPollDurablyStagesBeforeAck(t *testing.T) {
	root := t.TempDir()
	outbox, err := agentoutbox.Open(agentoutbox.Config{Dir: filepath.Join(root, "outbox"), MaxBytes: 2 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer outbox.Close()
	event, err := outbox.Append("test.v1", map[string]int{"n": 1}, agentoutbox.PriorityCritical)
	if err != nil {
		t.Fatal(err)
	}
	service, err := agentipc.Start(agentipc.Config{
		Endpoint: "tcp://127.0.0.1:0", Token: "test-token", Instance: "test",
		DiscoveryFile: filepath.Join(root, "discovery.json"), ServerVersion: "test", Outbox: outbox,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = service.Close(ctx)
	}()
	client, err := agentipc.NewClient(service.Endpoint(), "test-token", "test-agent")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	handshake, err := client.Handshake(context.Background(), "test", []string{"events.v1"})
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := agentinbox.Open(filepath.Join(root, "inbox", "events.log"), 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer inbox.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- poll(ctx, client, inbox, nil, handshake.AckedSequence) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if outbox.Stats().AckedSequence == event.Sequence {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if outbox.Stats().AckedSequence != event.Sequence {
		cancel()
		t.Fatalf("server ACK = %d, want %d", outbox.Stats().AckedSequence, event.Sequence)
	}
	if inbox.LastSequence() != event.Sequence {
		cancel()
		t.Fatalf("inbox sequence = %d, want %d", inbox.LastSequence(), event.Sequence)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPollDoesNotAckBeforeWebhookProcessingSucceeds(t *testing.T) {
	root := t.TempDir()
	outbox, err := agentoutbox.Open(agentoutbox.Config{Dir: filepath.Join(root, "outbox"), MaxBytes: 2 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer outbox.Close()
	_, err = outbox.Append(agentproto.EventRoomDisbandedV1, agentproto.RoomEventV1{Server: "test", RoomID: "R"}, agentoutbox.PriorityCritical)
	if err != nil {
		t.Fatal(err)
	}
	service, err := agentipc.Start(agentipc.Config{Endpoint: "tcp://127.0.0.1:0", Token: "token", DiscoveryFile: filepath.Join(root, "discovery"), Outbox: outbox})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = service.Close(ctx)
	}()
	client, _ := agentipc.NewClient(service.Endpoint(), "token", "agent")
	defer client.Close()
	handshake, err := client.Handshake(context.Background(), "test", []string{"events.v1"})
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := agentinbox.Open(filepath.Join(root, "inbox", "events.log"), 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer inbox.Close()
	deliverer := &failingWebhookDeliverer{fail: true}
	processor, err := agentwebhook.OpenProcessor(inbox, deliverer, nil, nil, filepath.Join(root, "cursor"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	err = poll(ctx, client, inbox, []eventProcessor{processor}, handshake.AckedSequence)
	cancel()
	if err == nil || outbox.Stats().AckedSequence != 0 {
		t.Fatalf("failed webhook should keep server event pending: ack=%d err=%v", outbox.Stats().AckedSequence, err)
	}
	deliverer.fail = false
	ctx, cancel = context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- poll(ctx, client, inbox, []eventProcessor{processor}, 0) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && outbox.Stats().AckedSequence == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if outbox.Stats().AckedSequence != 1 {
		t.Fatalf("successful webhook did not ACK event: %+v", outbox.Stats())
	}
}
