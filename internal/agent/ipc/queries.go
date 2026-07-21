package agentipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/common/agentproto"
	"github.com/google/uuid"
)

var ErrAgentUnavailable = errors.New("Agent query service unavailable")

type queryState struct {
	request  agentproto.QueryRequest
	response chan agentproto.QueryResponse
	claimed  time.Time
}

type queryBroker struct {
	mu      sync.Mutex
	pending map[string]*queryState
	order   []string
	max     int
}

func newQueryBroker(max int) *queryBroker {
	return &queryBroker{pending: make(map[string]*queryState), max: max}
}

func (b *queryBroker) submit(ctx context.Context, method string, params any) (agentproto.QueryResponse, error) {
	data, err := json.Marshal(params)
	if err != nil {
		return agentproto.QueryResponse{}, err
	}
	request := agentproto.QueryRequest{ID: uuid.NewString(), Method: method, Params: data}
	state := &queryState{request: request, response: make(chan agentproto.QueryResponse, 1)}
	b.mu.Lock()
	if len(b.pending) >= b.max {
		b.mu.Unlock()
		return agentproto.QueryResponse{}, ErrAgentUnavailable
	}
	b.pending[request.ID] = state
	b.order = append(b.order, request.ID)
	b.mu.Unlock()
	defer b.remove(request.ID)
	select {
	case response := <-state.response:
		return response, nil
	case <-ctx.Done():
		return agentproto.QueryResponse{}, ctx.Err()
	}
}

func (b *queryBroker) claim() (agentproto.QueryRequest, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	for _, id := range b.order {
		state := b.pending[id]
		if state == nil {
			continue
		}
		if state.claimed.IsZero() || now.Sub(state.claimed) > 10*time.Second {
			state.claimed = now
			return state.request, true
		}
	}
	return agentproto.QueryRequest{}, false
}

func (b *queryBroker) complete(response agentproto.QueryResponse) error {
	b.mu.Lock()
	state := b.pending[response.ID]
	b.mu.Unlock()
	if state == nil {
		return fmt.Errorf("unknown Agent query %q", response.ID)
	}
	select {
	case state.response <- response:
		return nil
	default:
		return nil
	}
}

func (b *queryBroker) remove(id string) {
	b.mu.Lock()
	delete(b.pending, id)
	if len(b.order) > b.max*2 {
		filtered := b.order[:0]
		for _, candidate := range b.order {
			if b.pending[candidate] != nil {
				filtered = append(filtered, candidate)
			}
		}
		b.order = filtered
	}
	b.mu.Unlock()
}
