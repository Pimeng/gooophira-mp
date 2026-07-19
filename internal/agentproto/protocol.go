// Package agentproto defines the versioned wire contract shared by the server
// and the optional Agent. It intentionally has no dependencies on server or
// extension implementation packages.
package agentproto

import (
	"encoding/json"
	"time"
)

const (
	ProtocolVersion = 1

	HeaderProtocolVersion = "X-Phira-Agent-Protocol"
	HeaderConsumerID      = "X-Phira-Agent-Consumer"
)

const (
	ErrorUnauthorized         = "unauthorized"
	ErrorProtocolIncompatible = "protocol-incompatible"
	ErrorConsumerConflict     = "consumer-conflict"
	ErrorInvalidRequest       = "invalid-request"
	ErrorAckOutOfRange        = "ack-out-of-range"
	ErrorAckGap               = "ack-gap"
)

const (
	EventGameStartedV1        = "game.started.v1"
	EventGameEndedV1          = "game.ended.v1"
	EventMatchFinishedV1      = "match.finished.v1"
	EventScoreSubmittedV1     = "score.submitted.v1"
	EventRoomCreatedV1        = "room.created.v1"
	EventRoomDisbandedV1      = "room.disbanded.v1"
	EventUserJoinedV1         = "user.joined.v1"
	EventMaintenanceChangedV1 = "maintenance.changed.v1"
	EventReplayCompletedV1    = "replay.completed.v1"
)

// Envelope is the stable event container used across the process boundary.
type Envelope struct {
	Version   int             `json:"version"`
	ID        string          `json:"id"`
	Sequence  uint64          `json:"sequence"`
	Type      string          `json:"type"`
	CreatedAt time.Time       `json:"created_at"`
	Payload   json.RawMessage `json:"payload"`
}

type InfoResponse struct {
	ProtocolVersion int      `json:"protocol_version"`
	ServerVersion   string   `json:"server_version"`
	Transport       string   `json:"transport"`
	Capabilities    []string `json:"capabilities"`
}

type HealthResponse struct {
	OK              bool      `json:"ok"`
	ProtocolVersion int       `json:"protocol_version"`
	ServerTime      time.Time `json:"server_time"`
}

type HandshakeRequest struct {
	ConsumerID   string   `json:"consumer_id"`
	AgentVersion string   `json:"agent_version"`
	Capabilities []string `json:"capabilities"`
}

type HandshakeResponse struct {
	OK              bool     `json:"ok"`
	ProtocolVersion int      `json:"protocol_version"`
	ServerVersion   string   `json:"server_version"`
	Capabilities    []string `json:"capabilities"`
	AckedSequence   uint64   `json:"acked_sequence"`
}

type EventsResponse struct {
	Events         []Envelope `json:"events"`
	AckedSequence  uint64     `json:"acked_sequence"`
	LatestSequence uint64     `json:"latest_sequence"`
}

type AckRequest struct {
	Sequence uint64 `json:"sequence"`
}

type AckResponse struct {
	OK       bool   `json:"ok"`
	Sequence uint64 `json:"sequence"`
}

type QueryRequest struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type QueryResponse struct {
	ID         string          `json:"id"`
	StatusCode int             `json:"status_code"`
	Body       json.RawMessage `json:"body"`
}

const (
	QueryStatsPlayer      = "stats.player"
	QueryStatsRecent      = "stats.recent"
	QueryStatsLeaderboard = "stats.leaderboard"
	QueryStatsChart       = "stats.chart"
	QueryStatsChartsHot   = "stats.charts_hot"
	QueryReplayUpload     = "replay.upload"
	QueryReplayAutoConfig = "replay.auto_config"
)

type ReplayUploadParams struct {
	ReplayID string `json:"replay_id"`
	Visible  bool   `json:"visible"`
}

type ReplayAutoConfigParams struct {
	UserID int   `json:"user_id"`
	Show   *bool `json:"show,omitempty"`
}

type StatsLeaderboardEntry struct {
	Rank        int     `json:"rank"`
	UserID      int     `json:"user_id"`
	Name        string  `json:"name"`
	Games       int     `json:"games"`
	Wins        int     `json:"wins"`
	AvgAcc      float64 `json:"avg_acc"`
	BestScore   int     `json:"best_score"`
	TotalScore  int64   `json:"total_score"`
	PlayTimeSec int     `json:"play_time_sec"`
	Rating      float64 `json:"rating"`
}

type StatsChart struct {
	ID           int     `json:"id"`
	Name         string  `json:"name"`
	Plays        int     `json:"plays"`
	AvgAcc       float64 `json:"avg_acc"`
	PassRate     float64 `json:"pass_rate"`
	LastPlayedAt string  `json:"last_played_at"`
	Popularity   float64 `json:"popularity"`
}

type ErrorResponse struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// Discovery is written by the server with user-only permissions so an Agent
// can find an auto-selected endpoint and its bearer token.
type Discovery struct {
	ProtocolVersion int       `json:"protocol_version"`
	Endpoint        string    `json:"endpoint"`
	Token           string    `json:"token"`
	Instance        string    `json:"instance"`
	PID             int       `json:"pid"`
	CreatedAt       time.Time `json:"created_at"`
}

type ChartV1 struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Difficulty   string `json:"difficulty,omitempty"`
	Charter      string `json:"charter,omitempty"`
	Illustration string `json:"illustration,omitempty"`
}

type PlayerV1 struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type MatchPlayerResultV1 struct {
	Player    PlayerV1 `json:"player"`
	Score     int      `json:"score"`
	Accuracy  float64  `json:"accuracy"`
	Perfect   int      `json:"perfect"`
	Good      int      `json:"good"`
	Bad       int      `json:"bad"`
	Miss      int      `json:"miss"`
	MaxCombo  int      `json:"max_combo"`
	FullCombo bool     `json:"full_combo"`
	Std       *float64 `json:"std,omitempty"`
	StdScore  *float64 `json:"std_score,omitempty"`
	Rank      int      `json:"rank"`
	RecordID  int      `json:"record_id,omitempty"`
	ReplayID  string   `json:"replay_id,omitempty"`
}

type GameStartedV1 struct {
	Server  string     `json:"server"`
	RoomID  string     `json:"room_id"`
	Chart   ChartV1    `json:"chart"`
	Players []PlayerV1 `json:"players"`
}

type GameEndedV1 struct {
	Server string  `json:"server"`
	RoomID string  `json:"room_id"`
	Chart  ChartV1 `json:"chart"`
}

type MatchFinishedV1 struct {
	Server          string                `json:"server"`
	RoomID          string                `json:"room_id"`
	Chart           ChartV1               `json:"chart"`
	StartedAt       time.Time             `json:"started_at"`
	DurationSeconds float64               `json:"duration_seconds"`
	Results         []MatchPlayerResultV1 `json:"results"`
}

type RoomEventV1 struct {
	Server    string    `json:"server"`
	RoomID    string    `json:"room_id"`
	UserCount int       `json:"user_count,omitempty"`
	User      *PlayerV1 `json:"user,omitempty"`
}

type ScoreSubmittedV1 struct {
	Server string                `json:"server"`
	RoomID string                `json:"room_id"`
	Chart  ChartV1               `json:"chart"`
	Ranks  []MatchPlayerResultV1 `json:"ranks"`
}

type MaintenanceChangedV1 struct {
	Server  string `json:"server"`
	Enabled bool   `json:"enabled"`
	Message string `json:"message,omitempty"`
}

type ReplayCompletedV1 struct {
	Server   string `json:"server"`
	RoomID   string `json:"room_id"`
	ReplayID string `json:"replay_id"`
	UserID   int    `json:"user_id"`
	ChartID  int    `json:"chart_id"`
}
