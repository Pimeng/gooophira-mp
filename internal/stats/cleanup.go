package stats

import (
	"fmt"
	"os"
)

// CleanupDetail 删除超过 retentionDays 天的明细（match_results 及孤儿 matches）。
// rollup 表不受影响——聚合已落账，裁剪明细不丢终身统计。
func (s *Store) CleanupDetail(retentionDays int) error {
	if retentionDays <= 0 {
		return nil
	}

	// 先删 match_results，再清理没有 match_results 的孤儿 matches。
	cutoff := fmt.Sprintf("-%d days", retentionDays)

	res, err := s.db.Exec(
		`DELETE FROM match_results WHERE match_id IN (
			SELECT id FROM matches WHERE started_at < datetime('now', ?)
		)`, cutoff,
	)
	if err != nil {
		return fmt.Errorf("stats: cleanup match_results: %w", err)
	}
	n, _ := res.RowsAffected()

	// 清理孤儿 matches（已无 match_results 的 match 行）
	if _, err := s.db.Exec(
		`DELETE FROM matches WHERE id NOT IN (SELECT DISTINCT match_id FROM match_results)`,
	); err != nil {
		return fmt.Errorf("stats: cleanup orphan matches: %w", err)
	}

	// 回收磁盘空间
	if _, err := s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return fmt.Errorf("stats: checkpoint: %w", err)
	}

	_ = n // 调用方可按需记录
	return nil
}

// VacuumIfNeeded 检查 DB 文件大小；超出 maxMB 时执行 VACUUM 回收空间。
func (s *Store) VacuumIfNeeded(dbPath string, maxMB int) error {
	if maxMB <= 0 || dbPath == "" {
		return nil
	}
	info, err := os.Stat(dbPath)
	if err != nil {
		return fmt.Errorf("stats: stat db: %w", err)
	}
	if info.Size() < int64(maxMB)*1024*1024 {
		return nil
	}
	if _, err := s.db.Exec("VACUUM"); err != nil {
		return fmt.Errorf("stats: vacuum: %w", err)
	}
	return nil
}
