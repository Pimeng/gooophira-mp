package agentoutbox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTestStore(t *testing.T, dir string, maxBytes int64) *Store {
	t.Helper()
	store, err := Open(Config{Dir: dir, MaxBytes: maxBytes, Now: func() time.Time { return time.Unix(123, 0) }})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestStorePersistsSequenceAndAck(t *testing.T) {
	dir := t.TempDir()
	store := openTestStore(t, dir, 2<<20)
	first, err := store.Append("test.v1", map[string]int{"n": 1}, PriorityCritical)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Append("test.v1", map[string]int{"n": 2}, PriorityCritical)
	if err != nil {
		t.Fatal(err)
	}
	if first.Sequence != 1 || second.Sequence != 2 || first.ID == second.ID {
		t.Fatalf("bad envelopes: %+v %+v", first, second)
	}
	if err := store.Ack(1); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store = openTestStore(t, dir, 2<<20)
	defer store.Close()
	events, acked, latest, err := store.Events(1, 10)
	if err != nil || acked != 1 || latest != 2 || len(events) != 1 || events[0].Sequence != 2 {
		t.Fatalf("recovered events=%+v acked=%d latest=%d err=%v", events, acked, latest, err)
	}
	third, err := store.Append("test.v1", nil, PriorityCritical)
	if err != nil || third.Sequence != 3 {
		t.Fatalf("third=%+v err=%v", third, err)
	}
	if err := store.Ack(3); err != nil {
		t.Fatal(err)
	}
	if err := store.Ack(3); err != nil {
		t.Fatalf("duplicate ACK should be idempotent: %v", err)
	}
}

func TestStoreRecoversPartialAndCorruptTail(t *testing.T) {
	for _, test := range []struct {
		name string
		tail []byte
	}{
		{name: "partial", tail: []byte{0, 0, 0}},
		{name: "bad crc", tail: []byte{0, 0, 0, 2, 0, 0, 0, 0, '{', '}'}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			store := openTestStore(t, dir, 2<<20)
			if _, err := store.Append("test.v1", map[string]string{"ok": "yes"}, PriorityCritical); err != nil {
				t.Fatal(err)
			}
			goodSize := store.Stats().Bytes
			store.Close()
			path := filepath.Join(dir, logName)
			file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = file.Write(test.tail)
			_ = file.Close()
			store = openTestStore(t, dir, 2<<20)
			defer store.Close()
			if store.Stats().Bytes != goodSize || store.Stats().LatestSequence != 1 {
				t.Fatalf("tail not recovered: %+v", store.Stats())
			}
		})
	}
}

func TestStoreCapacityReservesCriticalSpace(t *testing.T) {
	store := openTestStore(t, t.TempDir(), 1<<20)
	defer store.Close()
	payload := map[string]string{"data": strings.Repeat("x", 180000)}
	for {
		_, err := store.Append("normal.v1", payload, PriorityNormal)
		if errors.Is(err, ErrFull) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if store.Stats().DroppedNormal == 0 {
		t.Fatal("normal capacity drop was not counted")
	}
	if _, err := store.Append("critical.v1", payload, PriorityCritical); err != nil {
		t.Fatalf("reserved critical capacity unavailable: %v", err)
	}
}

func TestStoreRejectsAckGapAndWrongAfter(t *testing.T) {
	store := openTestStore(t, t.TempDir(), 2<<20)
	defer store.Close()
	_, _ = store.Append("test.v1", nil, PriorityCritical)
	if err := store.Ack(2); !errors.Is(err, ErrAckOutOfRange) {
		t.Fatalf("ACK out of range error = %v", err)
	}
	if _, _, _, err := store.Events(1, 10); err == nil {
		t.Fatal("wrong after sequence was accepted")
	}
}

func TestStoreRecoversCheckpointBeforeCompaction(t *testing.T) {
	dir := t.TempDir()
	store := openTestStore(t, dir, 2<<20)
	first, _ := store.Append("test.v1", map[string]int{"n": 1}, PriorityCritical)
	second, _ := store.Append("test.v1", map[string]int{"n": 2}, PriorityCritical)
	// 模拟原子检查点完成后、日志压缩前发生崩溃。
	if err := store.writeCheckpoint(first.Sequence); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = openTestStore(t, dir, 2<<20)
	defer store.Close()
	events, acked, latest, err := store.Events(first.Sequence, 10)
	if err != nil || acked != first.Sequence || latest != second.Sequence || len(events) != 1 || events[0].ID != second.ID {
		t.Fatalf("crash-window recovery events=%+v acked=%d latest=%d err=%v", events, acked, latest, err)
	}
}
