package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/agentproto"
)

type StatsProvider interface {
	QueryStats(context.Context, string, any) (status int, body json.RawMessage, err error)
}

func (s *Service) queryStats(w http.ResponseWriter, method string, params any) bool {
	if s.statsProvider == nil {
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "stats-unavailable"})
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	status, body, err := s.statsProvider.QueryStats(ctx, method, params)
	if err != nil {
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "stats-unavailable"})
		return false
	}
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.Header().Set("x-content-type-options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(body)
	return true
}

func (s *Service) handlePlayer(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/player/")
	if rest, ok := strings.CutSuffix(idStr, "/recent"); ok {
		s.handlePlayerRecent(w, r, rest)
		return
	}
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		s.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid-player-id"})
		return
	}
	s.queryStats(w, agentproto.QueryStatsPlayer, map[string]int{"id": id})
}

func (s *Service) handlePlayerRecent(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		s.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid-player-id"})
		return
	}
	limit := 10
	if value, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && value > 0 && value <= 50 {
		limit = value
	}
	s.queryStats(w, agentproto.QueryStatsRecent, map[string]int{"id": id, "limit": limit})
}

func (s *Service) handleLeaderboard(w http.ResponseWriter, r *http.Request) {
	sortBy := r.URL.Query().Get("sort")
	if sortBy != "playtime" && sortBy != "score" {
		sortBy = "rating"
	}
	limit := 20
	if value, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && value > 0 && value <= 100 {
		limit = value
	}
	s.queryStats(w, agentproto.QueryStatsLeaderboard, map[string]any{"sort": sortBy, "limit": limit})
}

func (s *Service) handleChart(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/chart/"))
	if err != nil || id <= 0 {
		s.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid-chart-id"})
		return
	}
	s.queryStats(w, agentproto.QueryStatsChart, map[string]int{"id": id})
}

func (s *Service) handleChartsHot(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if value, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && value > 0 && value <= 100 {
		limit = value
	}
	s.queryStats(w, agentproto.QueryStatsChartsHot, map[string]int{"limit": limit})
}
