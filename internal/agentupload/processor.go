package agentupload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/Pimeng/gooophira-mp/internal/agentinbox"
	"github.com/Pimeng/gooophira-mp/internal/agentproto"
)

type Processor struct {
	mu         sync.Mutex
	inbox      *agentinbox.Store
	store      *Store
	cursorPath string
	cursor     uint64
}

func OpenProcessor(inbox *agentinbox.Store, store *Store, cursorPath string) (*Processor, error) {
	p := &Processor{inbox: inbox, store: store, cursorPath: cursorPath, cursor: inbox.BaselineSequence()}
	data, err := os.ReadFile(cursorPath)
	if err == nil {
		if _, err := fmt.Sscanf(string(data), "%d", &p.cursor); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if p.cursor < inbox.BaselineSequence() || p.cursor > inbox.LastSequence() {
		return nil, fmt.Errorf("Agent upload cursor outside inbox")
	}
	return p, nil
}

func (p *Processor) Process(_ context.Context, limit int) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	events, err := p.inbox.ReadAfter(p.cursor, limit)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, envelope := range events {
		if envelope.Type == agentproto.EventReplayCompletedV1 {
			var payload agentproto.ReplayCompletedV1
			if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
				return processed, err
			}
			if p.store.cfg.AutoUpload {
				if err := p.store.Schedule(payload.ReplayID, envelope.CreatedAt); err != nil {
					return processed, err
				}
			}
		}
		if err := writeCursor(p.cursorPath, envelope.Sequence); err != nil {
			return processed, err
		}
		p.cursor = envelope.Sequence
		processed++
	}
	return processed, nil
}

func (p *Processor) Cursor() uint64 { p.mu.Lock(); defer p.mu.Unlock(); return p.cursor }

func writeCursor(path string, sequence uint64) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil && filepath.Dir(path) != "." {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".upload-cursor-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := fmt.Fprintf(temp, "%d\n", sequence); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return replaceFile(tempPath, path)
}
