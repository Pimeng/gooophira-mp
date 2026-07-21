package agentipc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/agent/outbox"
	"github.com/Pimeng/gooophira-mp/internal/common/agentproto"
)

func startTestService(t *testing.T) (*Service, agentproto.Discovery, string) {
	return startTestServiceWithOutbox(t, nil)
}

func startTestServiceWithOutbox(t *testing.T, outbox *agentoutbox.Store) (*Service, agentproto.Discovery, string) {
	t.Helper()
	discoveryPath := filepath.Join(t.TempDir(), "agent-ipc.json")
	service, err := Start(Config{
		Endpoint: "tcp://127.0.0.1:0", DiscoveryFile: discoveryPath,
		Instance: "test", ServerVersion: "test-server", Outbox: outbox,
	})
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := ReadDiscovery(discoveryPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := service.Close(ctx); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return service, discovery, discoveryPath
}

func TestAgentLateStartPullAckAndResume(t *testing.T) {
	dir := t.TempDir()
	outbox, err := agentoutbox.Open(agentoutbox.Config{Dir: dir, MaxBytes: 2 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer outbox.Close()
	first, _ := outbox.Append("test.v1", map[string]int{"n": 1}, agentoutbox.PriorityCritical)
	second, _ := outbox.Append("test.v1", map[string]int{"n": 2}, agentoutbox.PriorityCritical)
	service, discovery, _ := startTestServiceWithOutbox(t, outbox)
	client, err := NewClient(discovery.Endpoint, discovery.Token, "late-agent")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	handshake, err := client.Handshake(context.Background(), "test-agent", []string{"events.v1"})
	if err != nil || handshake.AckedSequence != 0 {
		t.Fatalf("handshake = %+v, %v", handshake, err)
	}
	batch, err := client.Events(context.Background(), 0, 10)
	if err != nil || len(batch.Events) != 2 || batch.Events[0].ID != first.ID || batch.Events[1].ID != second.ID {
		t.Fatalf("batch = %+v, %v", batch, err)
	}
	if err := client.Ack(context.Background(), second.Sequence); err != nil {
		t.Fatal(err)
	}
	if err := client.Ack(context.Background(), second.Sequence); err != nil {
		t.Fatalf("duplicate ACK: %v", err)
	}
	third, _ := outbox.Append("test.v1", map[string]int{"n": 3}, agentoutbox.PriorityCritical)
	batch, err = client.Events(context.Background(), second.Sequence, 10)
	if err != nil || len(batch.Events) != 1 || batch.Events[0].ID != third.ID || batch.AckedSequence != second.Sequence {
		t.Fatalf("resumed batch = %+v, %v", batch, err)
	}
	if err := client.Ack(context.Background(), third.Sequence+1); err == nil || !strings.Contains(err.Error(), agentproto.ErrorAckGap) {
		t.Fatalf("undelivered ACK error = %v", err)
	}
	if _, err := client.Events(context.Background(), 0, 10); err == nil || !strings.Contains(err.Error(), agentproto.ErrorAckGap) {
		t.Fatalf("wrong after error = %v", err)
	}
	_ = service
}

func TestHandshakeHealthAndStatus(t *testing.T) {
	service, discovery, _ := startTestService(t)
	client, err := NewClient(discovery.Endpoint, discovery.Token, "agent-one")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	response, err := client.Handshake(context.Background(), "test-agent", []string{"health.v1"})
	if err != nil {
		t.Fatal(err)
	}
	if response.ProtocolVersion != agentproto.ProtocolVersion || response.ServerVersion != "test-server" {
		t.Fatalf("handshake = %+v", response)
	}
	if _, err := client.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	status := service.Status()
	if !status.Online || status.ConsumerID != "agent-one" || status.AgentVersion != "test-agent" {
		t.Fatalf("status = %+v", status)
	}
	events, err := client.Events(context.Background(), 0, 10)
	if err != nil || len(events.Events) != 0 || events.LatestSequence != 0 {
		t.Fatalf("empty Stage A outbox = %+v, %v", events, err)
	}
	if err := client.Ack(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
}

func TestRejectsBadTokenAndConcurrentConsumer(t *testing.T) {
	_, discovery, _ := startTestService(t)
	bad, err := NewClient(discovery.Endpoint, "wrong-token", "bad")
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()
	if _, err := bad.Handshake(context.Background(), "test", nil); err == nil || !strings.Contains(err.Error(), agentproto.ErrorUnauthorized) {
		t.Fatalf("bad token error = %v", err)
	}
	first, _ := NewClient(discovery.Endpoint, discovery.Token, "first")
	defer first.Close()
	if _, err := first.Handshake(context.Background(), "test", nil); err != nil {
		t.Fatal(err)
	}
	second, _ := NewClient(discovery.Endpoint, discovery.Token, "second")
	defer second.Close()
	if _, err := second.Handshake(context.Background(), "test", nil); err == nil || !strings.Contains(err.Error(), agentproto.ErrorConsumerConflict) {
		t.Fatalf("consumer conflict error = %v", err)
	}
}

func TestRejectsIncompatibleProtocolVersion(t *testing.T) {
	_, discovery, _ := startTestService(t)
	client, err := NewClient(discovery.Endpoint, discovery.Token, "future-agent")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.protocol = agentproto.ProtocolVersion + 1
	if _, err := client.Handshake(context.Background(), "test", nil); err == nil || !strings.Contains(err.Error(), agentproto.ErrorProtocolIncompatible) {
		t.Fatalf("protocol mismatch error = %v", err)
	}
}

func TestDiscoveryPermissionsAndCleanup(t *testing.T) {
	service, _, path := startTestService(t)
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("discovery mode = %o, want 600", info.Mode().Perm())
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("discovery file remains after close: %v", err)
	}
}

func TestAgentPullsAndCompletesServerQuery(t *testing.T) {
	service, discovery, _ := startTestService(t)
	client, err := NewClient(discovery.Endpoint, discovery.Token, "query-agent")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Handshake(context.Background(), "test", []string{"stats-query.v1"}); err != nil {
		t.Fatal(err)
	}
	type queryResult struct {
		status int
		body   []byte
		err    error
	}
	done := make(chan queryResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		status, body, err := service.QueryStats(ctx, "stats.test", map[string]int{"id": 1})
		done <- queryResult{status: status, body: body, err: err}
	}()
	deadline := time.Now().Add(time.Second)
	var query agentproto.QueryRequest
	var ok bool
	for time.Now().Before(deadline) {
		query, ok, err = client.NextQuery(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ok || query.Method != "stats.test" {
		t.Fatalf("claimed query = %+v ok=%v", query, ok)
	}
	if err := client.QueryResult(context.Background(), agentproto.QueryResponse{ID: query.ID, StatusCode: 200, Body: []byte(`{"ok":true}`)}); err != nil {
		t.Fatal(err)
	}
	result := <-done
	if result.err != nil || result.status != 200 || string(result.body) != `{"ok":true}` {
		t.Fatalf("query result = %+v", result)
	}
}

func TestServerQueryTimeoutRemovesPendingTask(t *testing.T) {
	service, discovery, _ := startTestService(t)
	client, _ := NewClient(discovery.Endpoint, discovery.Token, "query-agent")
	defer client.Close()
	_, _ = client.Handshake(context.Background(), "test", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, _, err := service.QueryStats(ctx, "stats.test", nil); err == nil {
		t.Fatal("query should time out without Agent result")
	}
	if _, ok, err := client.NextQuery(context.Background()); err != nil || ok {
		t.Fatalf("timed-out query remained claimable: ok=%v err=%v", ok, err)
	}
}
