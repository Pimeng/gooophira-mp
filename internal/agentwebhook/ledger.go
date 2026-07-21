package agentwebhook

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Pimeng/gooophira-mp/internal/securepath"
)

const ledgerHeaderSize = 8

type ledgerStatus string

const (
	ledgerSucceeded       ledgerStatus = "succeeded"
	ledgerPermanentFailed ledgerStatus = "permanent_failed"
)

type ledgerRecord struct {
	EventID  string       `json:"event_id"`
	TargetID string       `json:"target_id"`
	Status   ledgerStatus `json:"status"`
}

type Ledger struct {
	mu      sync.Mutex
	file    *os.File
	path    string
	entries map[string]ledgerStatus
}

func OpenLedger(path string) (*Ledger, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil && filepath.Dir(path) != "." {
		return nil, err
	}
	if filepath.Dir(path) != "." {
		if err := securepath.RestrictToCurrentUser(filepath.Dir(path)); err != nil {
			return nil, err
		}
	}
	ledger := &Ledger{path: path, entries: make(map[string]ledgerStatus)}
	if err := ledger.recover(path); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	ledger.file = file
	if err := securepath.RestrictToCurrentUser(path); err != nil {
		_ = file.Close()
		return nil, err
	}
	return ledger, nil
}

// CompleteEvent 在事件处理游标推进后删除各目标状态。清理前崩溃只会留下
// 无害的陈旧条目；清理后崩溃也安全，因为游标已经跳过该事件。
func (l *Ledger) CompleteEvent(eventID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	prefix := eventID + "\x00"
	changed := false
	for key := range l.entries {
		if strings.HasPrefix(key, prefix) {
			delete(l.entries, key)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return l.rewriteLocked()
}

func ledgerKey(eventID, targetID string) string { return eventID + "\x00" + targetID }

func (l *Ledger) Done(eventID, targetID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.entries[ledgerKey(eventID, targetID)]
	return ok
}

func (l *Ledger) Mark(eventID, targetID string, status ledgerStatus) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := ledgerKey(eventID, targetID)
	if _, exists := l.entries[key]; exists {
		return nil
	}
	record := ledgerRecord{EventID: eventID, TargetID: targetID, Status: status}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	encoded := make([]byte, ledgerHeaderSize)
	binary.BigEndian.PutUint32(encoded[:4], uint32(len(data)))
	binary.BigEndian.PutUint32(encoded[4:8], crc32.ChecksumIEEE(data))
	encoded = append(encoded, data...)
	if n, err := l.file.Write(encoded); err != nil || n != len(encoded) {
		if err == nil {
			err = io.ErrShortWrite
		}
		return err
	}
	if err := l.file.Sync(); err != nil {
		return err
	}
	l.entries[key] = status
	return nil
}

func (l *Ledger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

func (l *Ledger) recover(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	var offset int64
	for {
		header := make([]byte, ledgerHeaderSize)
		if _, err := io.ReadFull(reader, header); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			if errors.Is(err, io.ErrUnexpectedEOF) {
				return truncateLedger(file, offset)
			}
			return err
		}
		length := binary.BigEndian.Uint32(header[:4])
		if length == 0 || length > 1<<20 {
			return truncateLedger(file, offset)
		}
		data := make([]byte, int(length))
		if _, err := io.ReadFull(reader, data); err != nil || crc32.ChecksumIEEE(data) != binary.BigEndian.Uint32(header[4:]) {
			return truncateLedger(file, offset)
		}
		var record ledgerRecord
		if err := json.Unmarshal(data, &record); err != nil || record.EventID == "" || record.TargetID == "" {
			return truncateLedger(file, offset)
		}
		l.entries[ledgerKey(record.EventID, record.TargetID)] = record.Status
		offset += int64(ledgerHeaderSize) + int64(length)
	}
}

func truncateLedger(file *os.File, offset int64) error {
	if err := file.Truncate(offset); err != nil {
		return err
	}
	return file.Sync()
}

func (l *Ledger) rewriteLocked() (err error) {
	if err := l.file.Close(); err != nil {
		return err
	}
	l.file = nil
	defer func() {
		if l.file == nil {
			l.file, _ = os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		}
	}()
	temp, err := os.CreateTemp(filepath.Dir(l.path), ".webhook-ledger-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	ok := false
	defer func() {
		_ = temp.Close()
		if !ok {
			_ = os.Remove(tempPath)
		}
	}()
	for key, status := range l.entries {
		parts := strings.SplitN(key, "\x00", 2)
		data, marshalErr := json.Marshal(ledgerRecord{EventID: parts[0], TargetID: parts[1], Status: status})
		if marshalErr != nil {
			return marshalErr
		}
		record := make([]byte, ledgerHeaderSize)
		binary.BigEndian.PutUint32(record[:4], uint32(len(data)))
		binary.BigEndian.PutUint32(record[4:8], crc32.ChecksumIEEE(data))
		record = append(record, data...)
		if _, err := temp.Write(record); err != nil {
			return err
		}
	}
	if err := securepath.RestrictToCurrentUser(tempPath); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := replaceFile(tempPath, l.path); err != nil {
		return err
	}
	if err := syncDir(filepath.Dir(l.path)); err != nil {
		return err
	}
	ok = true
	l.file, err = os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	return err
}
