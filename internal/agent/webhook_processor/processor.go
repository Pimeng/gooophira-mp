package agentwebhook

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/Pimeng/gooophira-mp/internal/agent/inbox"
	"github.com/Pimeng/gooophira-mp/internal/agent/integration/webhook"
	"github.com/Pimeng/gooophira-mp/internal/common/platform/securepath"
	"github.com/Pimeng/gooophira-mp/internal/common/webhookmodel"
	"github.com/Pimeng/gooophira-mp/internal/config"
)

type Deliverer interface {
	DeliverEvent(context.Context, webhookmodel.Event) error
}

type Planner interface {
	Plan(webhookmodel.Event) []webhook.TargetDelivery
	DeliverTarget(context.Context, config.WebhookTarget, webhookmodel.Event) (webhook.DeliveryOutcome, error)
}

type Processor struct {
	mu         sync.Mutex
	inbox      *agentinbox.Store
	deliverer  Deliverer
	planner    Planner
	ledger     *Ledger
	cursorPath string
	cursor     uint64
}

func OpenProcessor(inbox *agentinbox.Store, deliverer Deliverer, planner Planner, ledger *Ledger, cursorPath string) (*Processor, error) {
	p := &Processor{inbox: inbox, deliverer: deliverer, planner: planner, ledger: ledger, cursorPath: cursorPath, cursor: inbox.BaselineSequence()}
	data, err := os.ReadFile(cursorPath)
	if err == nil {
		if _, err := fmt.Sscanf(string(data), "%d", &p.cursor); err != nil {
			return nil, fmt.Errorf("decode Agent webhook cursor: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if p.cursor < inbox.BaselineSequence() || p.cursor > inbox.LastSequence() {
		return nil, fmt.Errorf("Agent webhook cursor %d outside inbox [%d,%d]", p.cursor, inbox.BaselineSequence(), inbox.LastSequence())
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
		event, deliver, err := Convert(envelope)
		if err != nil {
			return processed, err
		}
		if deliver && p.deliverer != nil {
			if p.planner != nil && p.ledger != nil {
				for _, target := range p.planner.Plan(event) {
					if p.ledger.Done(envelope.ID, target.ID) {
						continue
					}
					outcome, err := p.planner.DeliverTarget(ctx, target.Target, event)
					if outcome == webhook.DeliveryRetryableFailure {
						return processed, err
					}
					status := ledgerSucceeded
					if outcome == webhook.DeliveryPermanentFailure {
						status = ledgerPermanentFailed
					}
					if err := p.ledger.Mark(envelope.ID, target.ID, status); err != nil {
						return processed, err
					}
				}
			} else if err := p.deliverer.DeliverEvent(ctx, event); err != nil {
				return processed, err
			}
		}
		if err := writeCursor(p.cursorPath, envelope.Sequence); err != nil {
			return processed, err
		}
		p.cursor = envelope.Sequence
		if p.ledger != nil {
			_ = p.ledger.CompleteEvent(envelope.ID)
		}
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
	temp, err := os.CreateTemp(filepath.Dir(path), ".webhook-cursor-*")
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
