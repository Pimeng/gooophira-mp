package agentinbox

import (
	"os"
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/common/agentproto"
)

func envelope(sequence uint64, id string) agentproto.Envelope {
	return agentproto.Envelope{Version: 1, Sequence: sequence, ID: id, Type: "test.v1", Payload: []byte(`{}`)}
}

func TestInboxIdempotencyAndRecovery(t *testing.T) {
	path := t.TempDir() + "/events.log"
	store, err := Open(path, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	if last, err := store.Accept([]agentproto.Envelope{envelope(1, "a"), envelope(2, "b")}); err != nil || last != 2 {
		t.Fatalf("Accept = %d, %v", last, err)
	}
	if last, err := store.Accept([]agentproto.Envelope{envelope(1, "a"), envelope(2, "b")}); err != nil || last != 2 {
		t.Fatalf("duplicate Accept = %d, %v", last, err)
	}
	if _, err := store.Accept([]agentproto.Envelope{envelope(2, "changed")}); err == nil {
		t.Fatal("changed event ID was accepted")
	}
	store.Close()
	file, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = file.Write([]byte{1, 2, 3})
	_ = file.Close()
	store, err = Open(path, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if store.LastSequence() != 2 {
		t.Fatalf("recovered sequence = %d", store.LastSequence())
	}
}

func TestInboxRejectsSequenceGap(t *testing.T) {
	store, err := Open(t.TempDir()+"/events.log", 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Accept([]agentproto.Envelope{envelope(2, "b")}); err == nil {
		t.Fatal("sequence gap was accepted")
	}
}

func TestInboxBaselinePersists(t *testing.T) {
	path := t.TempDir() + "/events.log"
	store, err := Open(path, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetBaseline(10); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Accept([]agentproto.Envelope{envelope(11, "next")}); err != nil {
		t.Fatal(err)
	}
	store.Close()
	store, err = Open(path, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if store.LastSequence() != 11 {
		t.Fatalf("baseline recovery sequence = %d", store.LastSequence())
	}
}
