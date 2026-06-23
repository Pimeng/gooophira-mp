package replay

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

var (
	errZstdUnsupported        = errors.New("replay-compression-zstd-unsupported")
	errCompressionUnsupported = errors.New("replay-compression-unsupported")
)

// DefaultBaseDir 返回默认回放根目录 <cwd>/record。
func DefaultBaseDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return filepath.Join(cwd, "record")
}

// FilePath 返回某用户某谱面某时刻回放文件路径：<base>/<userID>/<chartID>/<ts>.phirarec。
func FilePath(baseDir string, userID, chartID int, timestamp int64) string {
	return filepath.Join(baseDir, strconv.Itoa(userID), strconv.Itoa(chartID), fmt.Sprintf("%d.phirarec", timestamp))
}

func ensureDir(baseDir string, userID, chartID int) (string, error) {
	dir := filepath.Join(baseDir, strconv.Itoa(userID), strconv.Itoa(chartID))
	return dir, os.MkdirAll(dir, 0o755)
}
