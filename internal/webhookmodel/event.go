// Package webhookmodel defines the presentation event consumed by webhook
// adapters. It is independent from the real-time server domain and Agent wire
// protocol.
package webhookmodel

import "time"

const (
	EventGameStart      = "game_start"
	EventGameEnd        = "game_end"
	EventScoreSubmitted = "score_submitted"
	EventRoomCreate     = "room_create"
	EventRoomDisband    = "room_disband"
	EventUserJoin       = "user_join"
	EventMaintenance    = "maintenance"
)

type Event struct {
	Type      string    `json:"type"`
	Time      time.Time `json:"time"`
	Server    string    `json:"server"`
	RoomID    string    `json:"room_id,omitempty"`
	ChartID   int       `json:"chart_id,omitempty"`
	ChartName string    `json:"chart_name,omitempty"`
	UserID    int       `json:"user_id,omitempty"`
	UserName  string    `json:"user_name,omitempty"`
	UserCount int       `json:"user_count,omitempty"`
	Enabled   bool      `json:"enabled,omitempty"`
	Message   string    `json:"message,omitempty"`

	ChartDifficulty string           `json:"chart_difficulty,omitempty"`
	ChartCharter    string           `json:"chart_charter,omitempty"`
	PlayerList      string           `json:"player_list,omitempty"`
	ImageURL        string           `json:"image_url,omitempty"`
	PlayerScoreRank []ScoreRankEntry `json:"player_score_rank,omitempty"`
}

type ScoreRankEntry struct {
	PlayerID int     `json:"player_id,omitempty"`
	Player   string  `json:"player"`
	Score    int     `json:"score"`
	StdScore float64 `json:"std_score"`
}
