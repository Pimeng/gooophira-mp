// agentoutbox 包实现有界持久事件日志，避免给实时服务端二进制引入数据库依赖。
package agentoutbox

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/common/agentproto"
	"github.com/Pimeng/gooophira-mp/internal/common/platform/securepath"
	"github.com/google/uuid"
)

const (
	logName        = "events.log"
	checkpointName = "checkpoint.json"
	maxRecordSize  = 1 << 20
	headerSize     = 8
)

var (
	ErrFull          = errors.New("agent outbox is full")
	ErrAckOutOfRange = errors.New("agent outbox ACK is out of range")
)

type Priority uint8

const (
	PriorityNormal Priority = iota
	PriorityCritical
)

type Config struct {
	Dir      string
	MaxBytes int64
	Now      func() time.Time
}

type Stats struct {
	AckedSequence  uint64 `json:"acked_sequence"`
	LatestSequence uint64 `json:"latest_sequence"`
	PendingEvents  int    `json:"pending_events"`
	Bytes          int64  `json:"bytes"`
	DroppedNormal  uint64 `json:"dropped_normal"`
}

type checkpoint struct {
	AckedSequence uint64 `json:"acked_sequence"`
}

type Store struct {
	mu      sync.Mutex
	dir     string
	path    string
	max     int64
	now     func() time.Time
	file    *os.File
	events  []agentproto.Envelope
	acked   uint64
	latest  uint64
	bytes   int64
	dropped uint64
	closed  bool
	failed  error
}

func Open(cfg Config) (*Store, error) {
	if cfg.Dir == "" {
		return nil, errors.New("agent outbox directory is empty")
	}
	if cfg.MaxBytes < maxRecordSize {
		return nil, fmt.Errorf("agent outbox max bytes must be at least %d", maxRecordSize)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("agent outbox create directory: %w", err)
	}
	if err := securepath.RestrictToCurrentUser(cfg.Dir); err != nil {
		return nil, fmt.Errorf("agent outbox restrict directory: %w", err)
	}
	s := &Store{dir: cfg.Dir, path: filepath.Join(cfg.Dir, logName), max: cfg.MaxBytes, now: cfg.Now}
	if err := s.loadCheckpoint(); err != nil {
		return nil, err
	}
	if err := s.recoverLog(); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("agent outbox open log: %w", err)
	}
	s.file = file
	if err := securepath.RestrictToCurrentUser(s.path); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("agent outbox restrict log: %w", err)
	}
	return s, nil
}

func (s *Store) Append(eventType string, payload any, priority Priority) (agentproto.Envelope, error) {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return agentproto.Envelope{}, fmt.Errorf("agent outbox marshal payload: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return agentproto.Envelope{}, errors.New("agent outbox is closed")
	}
	if s.failed != nil {
		return agentproto.Envelope{}, s.failed
	}
	envelope := agentproto.Envelope{
		Version: agentproto.ProtocolVersion, ID: uuid.NewString(), Sequence: s.latest + 1,
		Type: eventType, CreatedAt: s.now().UTC(), Payload: payloadJSON,
	}
	record, err := encodeRecord(envelope)
	if err != nil {
		return agentproto.Envelope{}, err
	}
	limit := s.max
	if priority == PriorityNormal {
		limit -= s.max / 4
	}
	if s.bytes+int64(len(record)) > limit {
		if priority == PriorityNormal {
			s.dropped++
		}
		return agentproto.Envelope{}, ErrFull
	}
	if n, err := s.file.Write(record); err != nil || n != len(record) {
		if err == nil {
			err = io.ErrShortWrite
		}
		s.failed = fmt.Errorf("agent outbox append: %w", err)
		return agentproto.Envelope{}, s.failed
	}
	if err := s.file.Sync(); err != nil {
		s.failed = fmt.Errorf("agent outbox sync: %w", err)
		return agentproto.Envelope{}, s.failed
	}
	s.events = append(s.events, envelope)
	s.latest = envelope.Sequence
	s.bytes += int64(len(record))
	return envelope, nil
}

func (s *Store) Events(after uint64, limit int) ([]agentproto.Envelope, uint64, uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if after != s.acked {
		return nil, s.acked, s.latest, fmt.Errorf("requested sequence %d does not match ACK %d", after, s.acked)
	}
	if limit < 1 {
		return nil, s.acked, s.latest, errors.New("event limit must be positive")
	}
	count := min(limit, len(s.events))
	out := append([]agentproto.Envelope(nil), s.events[:count]...)
	return out, s.acked, s.latest, nil
}

func (s *Store) Ack(sequence uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sequence <= s.acked {
		return nil
	}
	if sequence > s.latest {
		return ErrAckOutOfRange
	}
	index := -1
	for i := range s.events {
		if s.events[i].Sequence == sequence {
			index = i
			break
		}
	}
	if index < 0 {
		return ErrAckOutOfRange
	}
	if err := s.writeCheckpoint(sequence); err != nil {
		return err
	}
	s.acked = sequence
	s.events = append([]agentproto.Envelope(nil), s.events[index+1:]...)
	return s.compactLocked()
}

func (s *Store) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{AckedSequence: s.acked, LatestSequence: s.latest, PendingEvents: len(s.events), Bytes: s.bytes, DroppedNormal: s.dropped}
}

func (s *Store) RecordDroppedNormal() {
	s.mu.Lock()
	s.dropped++
	s.mu.Unlock()
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.file == nil {
		return nil
	}
	return s.file.Close()
}

func encodeRecord(envelope agentproto.Envelope) ([]byte, error) {
	data, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	if len(data) > maxRecordSize {
		return nil, errors.New("agent outbox record is too large")
	}
	record := make([]byte, headerSize+len(data))
	binary.BigEndian.PutUint32(record[0:4], uint32(len(data)))
	binary.BigEndian.PutUint32(record[4:8], crc32.ChecksumIEEE(data))
	copy(record[headerSize:], data)
	return record, nil
}

func (s *Store) recoverLog() error {
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("agent outbox recover log: %w", err)
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	var offset int64
	var previous uint64
	first := true
	s.latest = s.acked
	for {
		header := make([]byte, headerSize)
		if _, err := io.ReadFull(reader, header); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if errors.Is(err, io.ErrUnexpectedEOF) {
				if err := truncateTail(file, offset); err != nil {
					return err
				}
				break
			}
			return err
		}
		length := binary.BigEndian.Uint32(header[:4])
		if length == 0 || length > maxRecordSize {
			if err := truncateTail(file, offset); err != nil {
				return err
			}
			break
		}
		data := make([]byte, int(length))
		if _, err := io.ReadFull(reader, data); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				if err := truncateTail(file, offset); err != nil {
					return err
				}
				break
			}
			return err
		}
		if crc32.ChecksumIEEE(data) != binary.BigEndian.Uint32(header[4:8]) {
			if err := truncateTail(file, offset); err != nil {
				return err
			}
			break
		}
		var envelope agentproto.Envelope
		if err := json.Unmarshal(data, &envelope); err != nil || envelope.Sequence == 0 || envelope.ID == "" {
			if err := truncateTail(file, offset); err != nil {
				return err
			}
			break
		}
		if first {
			if envelope.Sequence > s.acked+1 {
				if err := truncateTail(file, offset); err != nil {
					return err
				}
				break
			}
			previous = envelope.Sequence - 1
			first = false
		}
		if envelope.Sequence != previous+1 {
			if err := truncateTail(file, offset); err != nil {
				return err
			}
			break
		}
		previous = envelope.Sequence
		offset += int64(headerSize) + int64(length)
		if envelope.Sequence > s.latest {
			s.latest = envelope.Sequence
		}
		if envelope.Sequence > s.acked {
			s.events = append(s.events, envelope)
		}
	}
	s.bytes = offset
	return nil
}

func truncateTail(file *os.File, offset int64) error {
	if err := file.Truncate(offset); err != nil {
		return fmt.Errorf("agent outbox truncate corrupt tail: %w", err)
	}
	return file.Sync()
}

func (s *Store) loadCheckpoint() error {
	data, err := os.ReadFile(filepath.Join(s.dir, checkpointName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("agent outbox read checkpoint: %w", err)
	}
	var cp checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return fmt.Errorf("agent outbox decode checkpoint: %w", err)
	}
	s.acked = cp.AckedSequence
	return nil
}

func (s *Store) writeCheckpoint(sequence uint64) error {
	data, err := json.Marshal(checkpoint{AckedSequence: sequence})
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.dir, checkpointName), append(data, '\n'))
}

func (s *Store) compactLocked() (err error) {
	if err := s.file.Close(); err != nil {
		return err
	}
	s.file = nil
	defer func() {
		if s.file == nil {
			var reopenErr error
			s.file, reopenErr = os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			if err == nil {
				err = reopenErr
			}
		}
	}()
	temp, err := os.CreateTemp(s.dir, ".events-*")
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
	var size int64
	for _, envelope := range s.events {
		record, encodeErr := encodeRecord(envelope)
		if encodeErr != nil {
			return encodeErr
		}
		if _, err := temp.Write(record); err != nil {
			return err
		}
		size += int64(len(record))
	}
	if err := temp.Chmod(0o600); err != nil {
		return err
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
	if err := replaceFile(tempPath, s.path); err != nil {
		return err
	}
	if err := syncDir(s.dir); err != nil {
		return err
	}
	ok = true
	s.bytes = size
	s.file, err = os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	return err
}

func writeAtomic(path string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".checkpoint-*")
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
	if err := temp.Chmod(0o600); err != nil {
		return err
	}
	if err := securepath.RestrictToCurrentUser(tempPath); err != nil {
		return err
	}
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := replaceFile(tempPath, path); err != nil {
		return err
	}
	if err := syncDir(filepath.Dir(path)); err != nil {
		return err
	}
	ok = true
	return nil
}
