package stats

// schema 是 SQLite 建表 DDL（幂等：CREATE TABLE IF NOT EXISTS）。
// 对应 TS 设计的 users / matches / match_results / player_stats / chart_stats。
const schema = `
CREATE TABLE IF NOT EXISTS users (
    id        INTEGER PRIMARY KEY,
    name      TEXT    NOT NULL,
    last_seen TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS matches (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    room_id    TEXT    NOT NULL,
    chart_id   INTEGER NOT NULL DEFAULT 0,
    chart_name TEXT    NOT NULL DEFAULT '',
    started_at TEXT    NOT NULL DEFAULT (datetime('now')),
    n          INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS match_results (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    match_id   INTEGER NOT NULL REFERENCES matches(id),
    user_id    INTEGER NOT NULL,
    score      INTEGER NOT NULL DEFAULT 0,
    accuracy   REAL    NOT NULL DEFAULT 0.0,
    perfect    INTEGER NOT NULL DEFAULT 0,
    good       INTEGER NOT NULL DEFAULT 0,
    bad        INTEGER NOT NULL DEFAULT 0,
    miss       INTEGER NOT NULL DEFAULT 0,
    max_combo  INTEGER NOT NULL DEFAULT 0,
    full_combo INTEGER NOT NULL DEFAULT 0,
    std        REAL    NOT NULL DEFAULT 0.0,
    std_score  REAL    NOT NULL DEFAULT 0.0,
    rank       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_mr_match ON match_results(match_id);
CREATE INDEX IF NOT EXISTS idx_mr_user  ON match_results(user_id);

CREATE TABLE IF NOT EXISTS player_stats (
    user_id    INTEGER PRIMARY KEY,
    games      INTEGER NOT NULL DEFAULT 0,
    wins       INTEGER NOT NULL DEFAULT 0,
    sum_acc    REAL    NOT NULL DEFAULT 0.0,
    best_score INTEGER NOT NULL DEFAULT 0,
    rating     REAL    NOT NULL DEFAULT 1500.0,
    updated_at TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS chart_stats (
    chart_id   INTEGER PRIMARY KEY,
    chart_name TEXT    NOT NULL DEFAULT '',
    plays      INTEGER NOT NULL DEFAULT 0,
    sum_acc    REAL    NOT NULL DEFAULT 0.0,
    pass_rate  REAL    NOT NULL DEFAULT 0.0,
    updated_at TEXT    NOT NULL DEFAULT (datetime('now'))
);
`
