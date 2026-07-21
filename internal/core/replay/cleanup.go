package replay

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

var tsNameRe = regexp.MustCompile(`^(\d+)\.phirarec$`)

// CleanupExpired 删除 baseDir 下早于 ttlDays 天的回放文件，并清理随之变空的目录。
// 目录结构：<base>/<userID>/<chartID>/<timestampMs>.phirarec。对应 TS replayStorage.cleanupExpiredReplays。
func CleanupExpired(baseDir string, now time.Time, ttlDays int) {
	if ttlDays <= 0 {
		return
	}
	ttlMs := int64(ttlDays) * 24 * 60 * 60 * 1000
	nowMs := now.UnixMilli()

	users, err := os.ReadDir(baseDir)
	if err != nil {
		return
	}
	for _, ue := range users {
		if !ue.IsDir() || !isIntName(ue.Name()) {
			continue
		}
		userDir := filepath.Join(baseDir, ue.Name())
		charts, _ := os.ReadDir(userDir)
		for _, ce := range charts {
			if !ce.IsDir() || !isIntName(ce.Name()) {
				continue
			}
			chartDir := filepath.Join(userDir, ce.Name())
			files, _ := os.ReadDir(chartDir)
			for _, f := range files {
				m := tsNameRe.FindStringSubmatch(f.Name())
				if m == nil {
					continue
				}
				ts, err := strconv.ParseInt(m[1], 10, 64)
				if err != nil || ts <= 0 || nowMs-ts <= ttlMs {
					continue
				}
				_ = os.Remove(filepath.Join(chartDir, f.Name()))
			}
			removeIfEmpty(chartDir)
		}
		removeIfEmpty(userDir)
	}
}

func isIntName(name string) bool {
	_, err := strconv.Atoi(name)
	return err == nil
}

func removeIfEmpty(dir string) {
	if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
		_ = os.Remove(dir)
	}
}

// CleanupExpired 用录制器当前 baseDir 清理过期回放。
func (r *Recorder) CleanupExpired(now time.Time, ttlDays int) {
	r.mu.Lock()
	dir := r.baseDir
	r.mu.Unlock()
	CleanupExpired(dir, now, ttlDays)
}
