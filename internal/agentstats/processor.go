// Package agentstats consumes match events into SQLite with transactional
// event-id idempotency and serves stats queries for the server proxy.
package agentstats

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
	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/securepath"
	"github.com/Pimeng/gooophira-mp/internal/stats"
)

type Processor struct {
	mu         sync.Mutex
	inbox      *agentinbox.Store
	store      *stats.Store
	cursorPath string
	cursor     uint64
}

func OpenProcessor(inbox *agentinbox.Store, store *stats.Store, cursorPath string) (*Processor, error) {
	p := &Processor{inbox: inbox, store: store, cursorPath: cursorPath, cursor: inbox.BaselineSequence()}
	data, err := os.ReadFile(cursorPath)
	if err == nil {
		if _, err := fmt.Sscanf(string(data), "%d", &p.cursor); err != nil {
			return nil, fmt.Errorf("decode Agent stats cursor: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if p.cursor < inbox.BaselineSequence() || p.cursor > inbox.LastSequence() {
		return nil, fmt.Errorf("Agent stats cursor %d outside inbox [%d,%d]", p.cursor, inbox.BaselineSequence(), inbox.LastSequence())
	}
	return p, nil
}

func (p *Processor) Process(ctx context.Context, limit int) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	events, err := p.inbox.ReadAfter(p.cursor, limit)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, envelope := range events {
		if envelope.Type == agentproto.EventMatchFinishedV1 {
			var match agentproto.MatchFinishedV1
			if err := json.Unmarshal(envelope.Payload, &match); err != nil {
				return processed, err
			}
			userIDs := make([]int, 0, len(match.Results))
			results := make(map[int]config.RecordData, len(match.Results))
			names := make(map[int]string, len(match.Results))
			for _, result := range match.Results {
				id := result.Player.ID
				userIDs = append(userIDs, id)
				names[id] = result.Player.Name
				results[id] = config.RecordData{
					ID: result.RecordID, Player: id, Score: result.Score, Accuracy: result.Accuracy,
					Perfect: result.Perfect, Good: result.Good, Bad: result.Bad, Miss: result.Miss,
					MaxCombo: result.MaxCombo, FullCombo: result.FullCombo, Std: result.Std, StdScore: result.StdScore,
				}
			}
			if _, err := p.store.RecordMatchEvent(ctx, envelope.ID, match.RoomID, match.Chart.ID, match.Chart.Name, userIDs, results, names, match.DurationSeconds); err != nil {
				return processed, err
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

func (p *Processor) Cursor() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cursor
}

func writeCursor(path string, sequence uint64) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil && filepath.Dir(path) != "." {
		return err
	}
	if filepath.Dir(path) != "." {
		if err := securepath.RestrictToCurrentUser(filepath.Dir(path)); err != nil {
			return err
		}
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".stats-cursor-*")
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
	if err := securepath.RestrictToCurrentUser(tempPath); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(temp, "%d\n", sequence); err != nil {
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
