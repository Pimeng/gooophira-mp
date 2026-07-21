// agentinbox 包在服务端确认前，将事件可靠暂存到 Agent。
package agentinbox

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

	"github.com/Pimeng/gooophira-mp/internal/common/agentproto"
	"github.com/Pimeng/gooophira-mp/internal/common/platform/securepath"
)

const (
	headerSize    = 8
	maxRecordSize = 1 << 20
)

type Store struct {
	mu           sync.Mutex
	file         *os.File
	path         string
	baselinePath string
	maxBytes     int64
	bytes        int64
	baseline     uint64
	last         uint64
	ids          map[uint64]string
	failed       error
}

func (s *Store) BaselineSequence() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.baseline
}

func Open(path string, maxBytes int64) (*Store, error) {
	if path == "" || maxBytes < maxRecordSize {
		return nil, errors.New("agent inbox requires a path and at least 1 MiB capacity")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil && filepath.Dir(path) != "." {
		return nil, err
	}
	if filepath.Dir(path) != "." {
		if err := securepath.RestrictToCurrentUser(filepath.Dir(path)); err != nil {
			return nil, err
		}
	}
	s := &Store{path: path, baselinePath: path + ".baseline", maxBytes: maxBytes, ids: make(map[uint64]string)}
	if err := s.loadBaseline(); err != nil {
		return nil, err
	}
	if err := s.recover(); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	s.file = file
	if err := securepath.RestrictToCurrentUser(path); err != nil {
		_ = file.Close()
		return nil, err
	}
	return s, nil
}

// SetBaseline 用服务端的持久化确认序号初始化完全为空的 inbox，
// 绝不会推进已经包含事件的 inbox。
func (s *Store) SetBaseline(sequence uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sequence <= s.last {
		return nil
	}
	if len(s.ids) != 0 || s.bytes != 0 {
		return fmt.Errorf("cannot advance non-empty Agent inbox from %d to %d", s.last, sequence)
	}
	data := []byte(fmt.Sprintf("%d\n", sequence))
	if err := writeBaselineAtomic(s.baselinePath, data); err != nil {
		return err
	}
	s.last = sequence
	s.baseline = sequence
	return nil
}

func (s *Store) LastSequence() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

// ReadAfter 返回指定序号之后的持久化事件，无需把整个有界 inbox 加载到内存。
func (s *Store) ReadAfter(sequence uint64, limit int) ([]agentproto.Envelope, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sequence < s.baseline || sequence > s.last {
		return nil, fmt.Errorf("Agent inbox cursor %d outside [%d,%d]", sequence, s.baseline, s.last)
	}
	if limit < 1 {
		return nil, errors.New("Agent inbox read limit must be positive")
	}
	file, err := os.Open(s.path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	out := make([]agentproto.Envelope, 0, limit)
	for len(out) < limit {
		header := make([]byte, headerSize)
		if _, err := io.ReadFull(reader, header); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		length := binary.BigEndian.Uint32(header[:4])
		if length == 0 || length > maxRecordSize {
			return nil, errors.New("Agent inbox contains an invalid record length")
		}
		data := make([]byte, int(length))
		if _, err := io.ReadFull(reader, data); err != nil {
			return nil, err
		}
		if crc32.ChecksumIEEE(data) != binary.BigEndian.Uint32(header[4:]) {
			return nil, errors.New("Agent inbox record checksum mismatch")
		}
		var event agentproto.Envelope
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, err
		}
		if event.Sequence > sequence {
			out = append(out, event)
		}
	}
	return out, nil
}

// Accept 对每个新信封执行 fsync，并返回最高持久化序号。
// 重放信封只有在序号与事件 ID 都匹配时才会被接受。
func (s *Store) Accept(events []agentproto.Envelope) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil {
		return s.last, s.failed
	}
	for _, event := range events {
		if event.Sequence <= s.last {
			if s.ids[event.Sequence] != event.ID {
				return s.last, fmt.Errorf("agent inbox event identity changed at sequence %d", event.Sequence)
			}
			continue
		}
		if event.Sequence != s.last+1 {
			return s.last, fmt.Errorf("agent inbox sequence gap: got %d after %d", event.Sequence, s.last)
		}
		record, err := encode(event)
		if err != nil {
			return s.last, err
		}
		if s.bytes+int64(len(record)) > s.maxBytes {
			return s.last, errors.New("agent inbox is full")
		}
		if n, err := s.file.Write(record); err != nil || n != len(record) {
			if err == nil {
				err = io.ErrShortWrite
			}
			s.failed = fmt.Errorf("agent inbox append: %w", err)
			return s.last, s.failed
		}
		if err := s.file.Sync(); err != nil {
			s.failed = fmt.Errorf("agent inbox sync: %w", err)
			return s.last, s.failed
		}
		s.bytes += int64(len(record))
		s.last = event.Sequence
		s.ids[event.Sequence] = event.ID
	}
	return s.last, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	return err
}

func (s *Store) recover() error {
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	var offset int64
	for {
		header := make([]byte, headerSize)
		if _, err := io.ReadFull(reader, header); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if errors.Is(err, io.ErrUnexpectedEOF) {
				if err := truncate(file, offset); err != nil {
					return err
				}
				break
			}
			return err
		}
		length := binary.BigEndian.Uint32(header[:4])
		if length == 0 || length > maxRecordSize {
			if err := truncate(file, offset); err != nil {
				return err
			}
			break
		}
		data := make([]byte, int(length))
		if _, err := io.ReadFull(reader, data); err != nil || crc32.ChecksumIEEE(data) != binary.BigEndian.Uint32(header[4:]) {
			if err := truncate(file, offset); err != nil {
				return err
			}
			break
		}
		var event agentproto.Envelope
		if err := json.Unmarshal(data, &event); err != nil || event.Sequence != s.last+1 || event.ID == "" {
			if err := truncate(file, offset); err != nil {
				return err
			}
			break
		}
		s.last = event.Sequence
		s.ids[event.Sequence] = event.ID
		offset += int64(headerSize) + int64(length)
	}
	s.bytes = offset
	return nil
}

func (s *Store) loadBaseline() error {
	data, err := os.ReadFile(s.baselinePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var sequence uint64
	if _, err := fmt.Sscanf(string(data), "%d", &sequence); err != nil {
		return fmt.Errorf("decode Agent inbox baseline: %w", err)
	}
	s.last = sequence
	s.baseline = sequence
	return nil
}

func writeBaselineAtomic(path string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".baseline-*")
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

func encode(event agentproto.Envelope) ([]byte, error) {
	data, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	if len(data) > maxRecordSize {
		return nil, errors.New("agent inbox record is too large")
	}
	record := make([]byte, headerSize+len(data))
	binary.BigEndian.PutUint32(record[:4], uint32(len(data)))
	binary.BigEndian.PutUint32(record[4:8], crc32.ChecksumIEEE(data))
	copy(record[8:], data)
	return record, nil
}

func truncate(file *os.File, offset int64) error {
	if err := file.Truncate(offset); err != nil {
		return err
	}
	return file.Sync()
}
