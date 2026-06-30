// Package stats 持久化每局结算成绩（SQLite）并提供 rollup 聚合，支撑
// 玩家档案、排行榜、谱面热度榜等带外数据产品。
//
// 对应 TS match-stats-design.md 的数据模型（归一化 + 增量 rollup）。
package stats

// MatchResult 是单局单个玩家的结算成绩（落 match_results 表）。
type MatchResult struct {
	MatchID   int64
	UserID    int
	Score     int
	Accuracy  float64
	Perfect   int
	Good      int
	Bad       int
	Miss      int
	MaxCombo  int
	FullCombo bool
	Std       float64
	StdScore  float64
	Rank      int // 本局内排名（1-based，按 score desc）
}

// PlayerStats 是玩家的终身聚合统计（player_stats 表行）。
type PlayerStats struct {
	UserID    int
	Games     int
	Wins      int
	SumAcc    float64
	BestScore int
	Rating    float64
	UpdatedAt string
}

// ChartStats 是谱面的聚合统计（chart_stats 表行）。
type ChartStats struct {
	ChartID   int
	Plays     int
	SumAcc    float64
	PassRate  float64
	UpdatedAt string
}
