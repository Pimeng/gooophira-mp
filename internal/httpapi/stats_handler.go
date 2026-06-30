package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Pimeng/gooophira-mp/internal/stats"
)

// requireStats 若 statsStore 未初始化，返回 503。
func (s *Service) requireStats(w http.ResponseWriter) bool {
	if s.statsStore == nil {
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"ok": false, "error": "stats-unavailable",
		})
		return false
	}
	return true
}

// ---------- GET /player/:id ----------

func (s *Service) handlePlayer(w http.ResponseWriter, r *http.Request) {
	if !s.requireStats(w) {
		return
	}
	// /player/1001 → "1001"
	idStr := strings.TrimPrefix(r.URL.Path, "/player/")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		s.writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok": false, "error": "invalid-player-id",
		})
		return
	}
	p, err := s.statsStore.GetPlayerProfile(id)
	if err != nil {
		s.writeJSON(w, http.StatusNotFound, map[string]any{
			"ok": false, "error": "not-found",
		})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"player": map[string]any{
			"id":             p.UserID,
			"games":          p.Games,
			"wins":           p.Wins,
			"avg_acc":        p.AvgAcc,
			"best_score":     p.BestScore,
			"total_score":    p.TotalScore,
			"play_time_sec":  p.PlayTimeSec,
			"rating":         p.Rating,
			"updated_at":     p.UpdatedAt,
		},
	})
}

// ---------- GET /leaderboard?sort=rating|playtime|score&limit=20 ----------

func (s *Service) handleLeaderboard(w http.ResponseWriter, r *http.Request) {
	if !s.requireStats(w) {
		return
	}
	q := r.URL.Query()
	sortBy := q.Get("sort")
	if sortBy == "" {
		sortBy = "rating"
	}
	limit := 20
	if l, err := strconv.Atoi(q.Get("limit")); err == nil && l > 0 && l <= 100 {
		limit = l
	}

	var entries []leaderboardJSON
	switch sortBy {
	case "playtime":
		lb, err := s.statsStore.GetLeaderboardByPlayTime(limit)
		if err != nil {
			s.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		entries = toLeaderboardJSON(lb)
	case "score":
		lb, err := s.statsStore.GetLeaderboardByTotalScore(limit)
		if err != nil {
			s.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		entries = toLeaderboardJSON(lb)
	default:
		lb, err := s.statsStore.GetLeaderboardByRating(limit)
		if err != nil {
			s.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		entries = toLeaderboardJSON(lb)
	}

	s.writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"sort":       sortBy,
		"leaderboard": entries,
	})
}

type leaderboardJSON struct {
	Rank        int     `json:"rank"`
	UserID      int     `json:"user_id"`
	Games       int     `json:"games"`
	Wins        int     `json:"wins"`
	AvgAcc      float64 `json:"avg_acc"`
	BestScore   int     `json:"best_score"`
	TotalScore  int64   `json:"total_score"`
	PlayTimeSec int     `json:"play_time_sec"`
	Rating      float64 `json:"rating"`
}

// ---------- GET /chart/:id ----------

func (s *Service) handleChart(w http.ResponseWriter, r *http.Request) {
	if !s.requireStats(w) {
		return
	}
	idStr := strings.TrimPrefix(r.URL.Path, "/chart/")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		s.writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok": false, "error": "invalid-chart-id",
		})
		return
	}
	c, err := s.statsStore.GetChartStats(id)
	if err != nil {
		s.writeJSON(w, http.StatusNotFound, map[string]any{
			"ok": false, "error": "not-found",
		})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"chart": map[string]any{
			"id":             c.ChartID,
			"name":           c.ChartName,
			"plays":          c.Plays,
			"avg_acc":        c.AvgAcc,
			"pass_rate":      c.PassRate,
			"last_played_at": c.LastPlayedAt,
			"popularity":     c.Popularity,
		},
	})
}

// ---------- GET /charts/hot?limit=20 ----------

func (s *Service) handleChartsHot(w http.ResponseWriter, r *http.Request) {
	if !s.requireStats(w) {
		return
	}
	limit := 20
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 && l <= 100 {
		limit = l
	}
	list, err := s.statsStore.GetChartPopularity(limit)
	if err != nil {
		s.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	charts := make([]map[string]any, 0, len(list))
	for _, c := range list {
		charts = append(charts, map[string]any{
			"id":             c.ChartID,
			"name":           c.ChartName,
			"plays":          c.Plays,
			"avg_acc":        c.AvgAcc,
			"pass_rate":      c.PassRate,
			"last_played_at": c.LastPlayedAt,
			"popularity":     c.Popularity,
		})
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"limit":  limit,
		"charts": charts,
	})
}

// ---------- helpers ----------

func toLeaderboardJSON(lb []stats.PlayerLeaderboard) []leaderboardJSON {
	out := make([]leaderboardJSON, 0, len(lb))
	for _, e := range lb {
		out = append(out, leaderboardJSON{
			Rank:        e.Rank,
			UserID:      e.UserID,
			Games:       e.Games,
			Wins:        e.Wins,
			AvgAcc:      e.AvgAcc,
			BestScore:   e.BestScore,
			TotalScore:  e.TotalScore,
			PlayTimeSec: e.PlayTimeSec,
			Rating:      e.Rating,
		})
	}
	return out
}
