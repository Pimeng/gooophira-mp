package replay

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/Pimeng/gooophira-mp/internal/common/protocol"
)

// ReplayHeader 是回放文件正文头部的元数据（解出正文后读取，不含触摸/判定数组）。
// 对应 TS replayStorage.readReplayHeader。
type ReplayHeader struct {
	RecordID  int
	Timestamp int64
	ChartID   int
	ChartName string
	UserID    int
	UserName  string
}

// ReplayEntry 是某谱面下一份回放的列表项。
type ReplayEntry struct {
	Timestamp int64
	RecordID  int
}

// ReadReplayHeader 读取回放文件并解析正文头部元数据（recordID/timestamp/chartID/chartName/
// userID/userName）。文件缺失/魔数不符/解压失败/越界均返回 error（不 panic）。
func ReadReplayHeader(path string) (*ReplayHeader, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !isPhiraRecordV2(raw) {
		return nil, errCompressionUnsupported
	}
	content, err := decodePayload(raw)
	if err != nil {
		return nil, err
	}
	// 正文字段顺序见 recorder.buildContent。用 DecodePacket 的 recover 把越界 panic 转为 error。
	h, err := protocol.DecodePacket(content, func(r *protocol.BinaryReader) ReplayHeader {
		return ReplayHeader{
			RecordID:  int(r.ReadI32()),
			Timestamp: r.ReadI64(),
			ChartID:   int(r.ReadI32()),
			ChartName: r.ReadString(),
			UserID:    int(r.ReadI32()),
			UserName:  r.ReadString(),
		}
	})
	if err != nil {
		return nil, err
	}
	return &h, nil
}

// ListReplaysForUser 列出某用户的全部本地回放，按 chartID 分组（每组按时间倒序）。
// 目录结构 <base>/<userID>/<chartID>/<timestampMs>.phirarec。无回放时返回空 map（非 error）。
func ListReplaysForUser(baseDir string, userID int) map[int][]ReplayEntry {
	result := make(map[int][]ReplayEntry)
	userDir := filepath.Join(baseDir, strconv.Itoa(userID))
	charts, err := os.ReadDir(userDir)
	if err != nil {
		return result // 无目录 = 无回放
	}
	for _, ce := range charts {
		if !ce.IsDir() {
			continue
		}
		chartID, err := strconv.Atoi(ce.Name())
		if err != nil {
			continue
		}
		files, _ := os.ReadDir(filepath.Join(userDir, ce.Name()))
		var entries []ReplayEntry
		for _, f := range files {
			m := tsNameRe.FindStringSubmatch(f.Name())
			if m == nil {
				continue
			}
			ts, err := strconv.ParseInt(m[1], 10, 64)
			if err != nil || ts <= 0 {
				continue
			}
			recordID := 0
			if h, herr := ReadReplayHeader(FilePath(baseDir, userID, chartID, ts)); herr == nil {
				recordID = h.RecordID
			}
			entries = append(entries, ReplayEntry{Timestamp: ts, RecordID: recordID})
		}
		if len(entries) == 0 {
			continue
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Timestamp > entries[j].Timestamp }) // 时间倒序
		result[chartID] = entries
	}
	return result
}

// DeleteReplayForUser 删除指定回放文件并清理随之变空的目录。文件不存在返回 (false, nil)。
func DeleteReplayForUser(baseDir string, userID, chartID int, timestamp int64) (bool, error) {
	path := FilePath(baseDir, userID, chartID, timestamp)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	removeIfEmpty(filepath.Join(baseDir, strconv.Itoa(userID), strconv.Itoa(chartID)))
	removeIfEmpty(filepath.Join(baseDir, strconv.Itoa(userID)))
	return true, nil
}
