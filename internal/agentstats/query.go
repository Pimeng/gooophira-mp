package agentstats

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Pimeng/gooophira-mp/internal/agentproto"
	"github.com/Pimeng/gooophira-mp/internal/stats"
)

type QueryHandler struct{ Store *stats.Store }

type idParams struct {
	ID    int `json:"id"`
	Limit int `json:"limit,omitempty"`
}

type leaderboardParams struct {
	Sort  string `json:"sort"`
	Limit int    `json:"limit"`
}

func (h QueryHandler) Handle(request agentproto.QueryRequest) agentproto.QueryResponse {
	if h.Store == nil {
		return queryJSON(request.ID, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "stats-unavailable"})
	}
	switch request.Method {
	case agentproto.QueryStatsPlayer:
		var params idParams
		if json.Unmarshal(request.Params, &params) != nil || params.ID <= 0 {
			return queryJSON(request.ID, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid-player-id"})
		}
		player, err := h.Store.GetPlayerProfile(params.ID)
		if err != nil {
			return queryStoreError(request.ID, err)
		}
		return queryJSON(request.ID, http.StatusOK, map[string]any{"ok": true, "player": map[string]any{
			"id": player.UserID, "name": player.Name, "games": player.Games, "wins": player.Wins,
			"avg_acc": player.AvgAcc, "best_score": player.BestScore, "total_score": player.TotalScore,
			"play_time_sec": player.PlayTimeSec, "rating": player.Rating, "updated_at": player.UpdatedAt,
		}})
	case agentproto.QueryStatsRecent:
		var params idParams
		if json.Unmarshal(request.Params, &params) != nil || params.ID <= 0 {
			return queryJSON(request.ID, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid-player-id"})
		}
		matches, err := h.Store.GetRecentMatches(params.ID, params.Limit)
		if err != nil {
			return queryStoreError(request.ID, err)
		}
		return queryJSON(request.ID, http.StatusOK, map[string]any{"ok": true, "user_id": params.ID, "recent": matches})
	case agentproto.QueryStatsLeaderboard:
		var params leaderboardParams
		if json.Unmarshal(request.Params, &params) != nil {
			return queryJSON(request.ID, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid-request"})
		}
		var entries []stats.PlayerLeaderboard
		var err error
		switch params.Sort {
		case "playtime":
			entries, err = h.Store.GetLeaderboardByPlayTime(params.Limit)
		case "score":
			entries, err = h.Store.GetLeaderboardByTotalScore(params.Limit)
		default:
			params.Sort = "rating"
			entries, err = h.Store.GetLeaderboardByRating(params.Limit)
		}
		if err != nil {
			return queryStoreError(request.ID, err)
		}
		out := make([]agentproto.StatsLeaderboardEntry, 0, len(entries))
		for _, entry := range entries {
			out = append(out, agentproto.StatsLeaderboardEntry{Rank: entry.Rank, UserID: entry.UserID, Name: entry.Name, Games: entry.Games, Wins: entry.Wins, AvgAcc: entry.AvgAcc, BestScore: entry.BestScore, TotalScore: entry.TotalScore, PlayTimeSec: entry.PlayTimeSec, Rating: entry.Rating})
		}
		return queryJSON(request.ID, http.StatusOK, map[string]any{"ok": true, "sort": params.Sort, "leaderboard": out})
	case agentproto.QueryStatsChart:
		var params idParams
		if json.Unmarshal(request.Params, &params) != nil || params.ID <= 0 {
			return queryJSON(request.ID, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid-chart-id"})
		}
		chart, err := h.Store.GetChartStats(params.ID)
		if err != nil {
			return queryStoreError(request.ID, err)
		}
		return queryJSON(request.ID, http.StatusOK, map[string]any{"ok": true, "chart": chartDTO(*chart)})
	case agentproto.QueryStatsChartsHot:
		var params idParams
		if json.Unmarshal(request.Params, &params) != nil {
			return queryJSON(request.ID, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid-request"})
		}
		charts, err := h.Store.GetChartPopularity(params.Limit)
		if err != nil {
			return queryStoreError(request.ID, err)
		}
		out := make([]agentproto.StatsChart, 0, len(charts))
		for _, chart := range charts {
			out = append(out, chartDTO(chart))
		}
		return queryJSON(request.ID, http.StatusOK, map[string]any{"ok": true, "limit": params.Limit, "charts": out})
	default:
		return queryJSON(request.ID, http.StatusNotFound, map[string]any{"ok": false, "error": "unknown-query"})
	}
}

func chartDTO(chart stats.ChartPopularity) agentproto.StatsChart {
	return agentproto.StatsChart{ID: chart.ChartID, Name: chart.ChartName, Plays: chart.Plays, AvgAcc: chart.AvgAcc, PassRate: chart.PassRate, LastPlayedAt: chart.LastPlayedAt, Popularity: chart.Popularity}
}

func queryStoreError(id string, err error) agentproto.QueryResponse {
	if errors.Is(err, sql.ErrNoRows) {
		return queryJSON(id, http.StatusNotFound, map[string]any{"ok": false, "error": "not-found"})
	}
	return queryJSON(id, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
}

func queryJSON(id string, status int, value any) agentproto.QueryResponse {
	body, _ := json.Marshal(value)
	return agentproto.QueryResponse{ID: id, StatusCode: status, Body: body}
}
