// Package agentipc implements the authenticated HTTP/JSON boundary exposed by
// the main server to the optional Agent.
package agentipc

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/agentoutbox"
	"github.com/Pimeng/gooophira-mp/internal/agentproto"
	"github.com/Pimeng/gooophira-mp/internal/agenttransport"
)

const (
	maxRequestBody = 64 << 10
	maxEventBatch  = 256
	consumerTTL    = 30 * time.Second
)

type Config struct {
	Endpoint      string
	Token         string
	DiscoveryFile string
	Instance      string
	ServerVersion string
	Outbox        *agentoutbox.Store
}

type Status struct {
	Enabled        bool      `json:"enabled"`
	Endpoint       string    `json:"endpoint,omitempty"`
	Online         bool      `json:"online"`
	ConsumerID     string    `json:"consumer_id,omitempty"`
	AgentVersion   string    `json:"agent_version,omitempty"`
	LastSeen       time.Time `json:"last_seen,omitempty"`
	AckedSequence  uint64    `json:"acked_sequence"`
	LatestSequence uint64    `json:"latest_sequence"`
	PendingEvents  int       `json:"pending_events"`
	OutboxBytes    int64     `json:"outbox_bytes"`
	DroppedNormal  uint64    `json:"dropped_normal"`
}

type Service struct {
	listener *agenttransport.Listener
	server   *http.Server
	token    string
	version  string
	outbox   *agentoutbox.Store
	queries  *queryBroker
	cleanup  func() error

	mu            sync.Mutex
	consumerID    string
	agentVersion  string
	lastSeen      time.Time
	lastDelivered uint64
	closeOnce     sync.Once
	closeErr      error
}

func Start(cfg Config) (*Service, error) {
	endpoint, err := agenttransport.Parse(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	if endpoint.Scheme == agenttransport.SchemeDisabled {
		return nil, nil
	}
	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		token, err = randomToken()
		if err != nil {
			return nil, err
		}
	}
	listener, err := agenttransport.Listen(endpoint, cfg.Instance)
	if err != nil {
		return nil, err
	}
	svc := &Service{listener: listener, token: token, version: cfg.ServerVersion, outbox: cfg.Outbox, queries: newQueryBroker(128)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /agent/v1/info", svc.handleInfo)
	mux.HandleFunc("GET /agent/v1/health", svc.handleHealth)
	mux.HandleFunc("POST /agent/v1/handshake", svc.handleHandshake)
	mux.HandleFunc("GET /agent/v1/events", svc.handleEvents)
	mux.HandleFunc("POST /agent/v1/events/ack", svc.handleAck)
	mux.HandleFunc("GET /agent/v1/queries/next", svc.handleNextQuery)
	mux.HandleFunc("POST /agent/v1/queries/result", svc.handleQueryResult)
	svc.server = &http.Server{
		Handler:           svc.middleware(mux),
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	discoveryPath := cfg.DiscoveryFile
	if discoveryPath == "" {
		discoveryPath = "agent-ipc.json"
	}
	svc.cleanup, err = writeDiscovery(discoveryPath, agentproto.Discovery{
		ProtocolVersion: agentproto.ProtocolVersion,
		Endpoint:        listener.Endpoint.String(),
		Token:           token,
		Instance:        cfg.Instance,
		PID:             os.Getpid(),
		CreatedAt:       time.Now().UTC(),
	})
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("agent IPC: write discovery: %w", err)
	}
	go func() {
		_ = svc.server.Serve(listener)
	}()
	return svc, nil
}

func (s *Service) Query(ctx context.Context, method string, params any) (agentproto.QueryResponse, error) {
	if !s.Status().Online {
		return agentproto.QueryResponse{}, ErrAgentUnavailable
	}
	return s.queries.submit(ctx, method, params)
}

func (s *Service) QueryStats(ctx context.Context, method string, params any) (int, json.RawMessage, error) {
	response, err := s.Query(ctx, method, params)
	if err != nil {
		return 0, nil, err
	}
	return response.StatusCode, response.Body, nil
}

func (s *Service) handleNextQuery(w http.ResponseWriter, r *http.Request) {
	if !s.touchConsumer(w, r) {
		return
	}
	query, ok := s.queries.claim()
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, query)
}

func (s *Service) handleQueryResult(w http.ResponseWriter, r *http.Request) {
	if !s.touchConsumer(w, r) {
		return
	}
	var response agentproto.QueryResponse
	if err := json.NewDecoder(r.Body).Decode(&response); err != nil || response.ID == "" || response.StatusCode < 100 || response.StatusCode > 599 {
		writeError(w, http.StatusBadRequest, agentproto.ErrorInvalidRequest, "invalid query result")
		return
	}
	if err := s.queries.complete(response); err != nil {
		// The originating HTTP request may have timed out after Agent claimed the
		// query. A late result is harmless and must not tear down Agent polling.
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true, "accepted": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true, "accepted": true})
}

func (s *Service) Endpoint() string { return s.listener.Endpoint.String() }

func (s *Service) Status() Status {
	s.mu.Lock()
	online := s.consumerID != "" && time.Since(s.lastSeen) <= consumerTTL
	status := Status{Enabled: true, Endpoint: s.Endpoint(), Online: online, ConsumerID: s.consumerID, AgentVersion: s.agentVersion, LastSeen: s.lastSeen}
	s.mu.Unlock()
	if s.outbox != nil {
		stats := s.outbox.Stats()
		status.AckedSequence, status.LatestSequence = stats.AckedSequence, stats.LatestSequence
		status.PendingEvents, status.OutboxBytes, status.DroppedNormal = stats.PendingEvents, stats.Bytes, stats.DroppedNormal
	}
	return status
}

func (s *Service) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		s.closeErr = s.server.Shutdown(ctx)
		if closeErr := s.listener.Close(); s.closeErr == nil && !errors.Is(closeErr, http.ErrServerClosed) && !errors.Is(closeErr, net.ErrClosed) {
			s.closeErr = closeErr
		}
		if s.cleanup != nil {
			if cleanupErr := s.cleanup(); s.closeErr == nil {
				s.closeErr = cleanupErr
			}
		}
	})
	return s.closeErr
}

func (s *Service) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if len(provided) != len(s.token) || subtle.ConstantTimeCompare([]byte(provided), []byte(s.token)) != 1 {
			writeError(w, http.StatusUnauthorized, agentproto.ErrorUnauthorized, "invalid Agent token")
			return
		}
		version, err := strconv.Atoi(r.Header.Get(agentproto.HeaderProtocolVersion))
		if err != nil || version != agentproto.ProtocolVersion {
			writeError(w, http.StatusConflict, agentproto.ErrorProtocolIncompatible, "unsupported Agent protocol version")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Service) handleInfo(w http.ResponseWriter, _ *http.Request) {
	capabilities := []string{"handshake.v1", "health.v1"}
	if s.outbox != nil {
		capabilities = append(capabilities, "events.v1")
	}
	writeJSON(w, http.StatusOK, agentproto.InfoResponse{
		ProtocolVersion: agentproto.ProtocolVersion,
		ServerVersion:   s.version,
		Transport:       string(s.listener.Endpoint.Scheme),
		Capabilities:    capabilities,
	})
}

func (s *Service) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !s.touchConsumer(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, agentproto.HealthResponse{OK: true, ProtocolVersion: agentproto.ProtocolVersion, ServerTime: time.Now().UTC()})
}

func (s *Service) handleHandshake(w http.ResponseWriter, r *http.Request) {
	var request agentproto.HandshakeRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || strings.TrimSpace(request.ConsumerID) == "" || len(request.ConsumerID) > 128 {
		writeError(w, http.StatusBadRequest, agentproto.ErrorInvalidRequest, "consumer_id is required")
		return
	}
	now := time.Now()
	s.mu.Lock()
	if s.consumerID != "" && s.consumerID != request.ConsumerID && now.Sub(s.lastSeen) <= consumerTTL {
		s.mu.Unlock()
		writeError(w, http.StatusConflict, agentproto.ErrorConsumerConflict, "another Agent consumer is active")
		return
	}
	acked := uint64(0)
	capabilities := []string{"handshake.v1", "health.v1"}
	if s.outbox != nil {
		acked = s.outbox.Stats().AckedSequence
		capabilities = append(capabilities, "events.v1")
	}
	s.consumerID, s.agentVersion, s.lastSeen, s.lastDelivered = request.ConsumerID, request.AgentVersion, now, acked
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, agentproto.HandshakeResponse{
		OK: true, ProtocolVersion: agentproto.ProtocolVersion, ServerVersion: s.version,
		Capabilities: capabilities, AckedSequence: acked,
	})
}

func (s *Service) handleEvents(w http.ResponseWriter, r *http.Request) {
	if !s.touchConsumer(w, r) {
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxEventBatch {
			writeError(w, http.StatusBadRequest, agentproto.ErrorInvalidRequest, "limit must be between 1 and 256")
			return
		}
		limit = parsed
	}
	after := uint64(0)
	if raw := r.URL.Query().Get("after"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, agentproto.ErrorInvalidRequest, "after must be an unsigned sequence")
			return
		}
		after = parsed
	}
	if s.outbox == nil {
		writeJSON(w, http.StatusOK, agentproto.EventsResponse{Events: []agentproto.Envelope{}})
		return
	}
	events, acked, latest, err := s.outbox.Events(after, limit)
	if err != nil {
		writeError(w, http.StatusConflict, agentproto.ErrorAckGap, err.Error())
		return
	}
	s.mu.Lock()
	if len(events) > 0 {
		s.lastDelivered = events[len(events)-1].Sequence
	} else {
		s.lastDelivered = acked
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, agentproto.EventsResponse{Events: events, AckedSequence: acked, LatestSequence: latest})
}

func (s *Service) handleAck(w http.ResponseWriter, r *http.Request) {
	if !s.touchConsumer(w, r) {
		return
	}
	var request agentproto.AckRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, agentproto.ErrorInvalidRequest, "invalid ACK body")
		return
	}
	if s.outbox == nil {
		if request.Sequence != 0 {
			writeError(w, http.StatusConflict, agentproto.ErrorAckOutOfRange, "outbox is not enabled")
			return
		}
		writeJSON(w, http.StatusOK, agentproto.AckResponse{OK: true})
		return
	}
	stats := s.outbox.Stats()
	s.mu.Lock()
	lastDelivered := s.lastDelivered
	s.mu.Unlock()
	if request.Sequence > lastDelivered {
		writeError(w, http.StatusConflict, agentproto.ErrorAckGap, "ACK sequence was not delivered")
		return
	}
	if err := s.outbox.Ack(request.Sequence); err != nil {
		code := agentproto.ErrorAckOutOfRange
		if request.Sequence > stats.LatestSequence {
			code = agentproto.ErrorAckOutOfRange
		}
		writeError(w, http.StatusConflict, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, agentproto.AckResponse{OK: true, Sequence: request.Sequence})
}

func (s *Service) touchConsumer(w http.ResponseWriter, r *http.Request) bool {
	consumer := strings.TrimSpace(r.Header.Get(agentproto.HeaderConsumerID))
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if consumer == "" || s.consumerID == "" || consumer != s.consumerID || now.Sub(s.lastSeen) > consumerTTL {
		writeError(w, http.StatusConflict, agentproto.ErrorConsumerConflict, "Agent handshake required")
		return false
	}
	s.lastSeen = now
	return true
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("agent IPC: generate token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, agentproto.ErrorResponse{OK: false, Code: code, Message: message})
}
