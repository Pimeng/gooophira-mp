package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/Pimeng/gooophira-mp/internal/common/protocol"
)

// 管理员数据持久化：全局封禁与房间级封禁列表落盘到 adminDataPath（JSON, version 1）。
// 对应 TS core/state.ts 的 loadAdminData / saveAdminData / flushAdminData。

type adminDataFile struct {
	Version         int              `json:"version"`
	BannedUsers     []int            `json:"bannedUsers"`
	BannedRoomUsers map[string][]int `json:"bannedRoomUsers"`
}

// LoadAdminData 从文件加载封禁数据；文件不存在或格式错误时静默忽略。
func (s *ServerState) LoadAdminData() error {
	if s.AdminDataPath == "" {
		return nil
	}
	data, err := os.ReadFile(s.AdminDataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var f adminDataFile
	if json.Unmarshal(data, &f) != nil || f.Version != 1 {
		return nil // 格式错误/版本不符：忽略
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	clear(s.BannedUsers)
	for _, id := range f.BannedUsers {
		s.BannedUsers[id] = struct{}{}
	}
	clear(s.BannedRoomUsers)
	for rid, ids := range f.BannedRoomUsers {
		set := make(map[int]struct{}, len(ids))
		for _, id := range ids {
			set[id] = struct{}{}
		}
		if len(set) > 0 {
			s.BannedRoomUsers[protocol.RoomID(rid)] = set
		}
	}
	return nil
}

// snapshotAdminData 生成可序列化的封禁快照（调用方须持 Mu），id 排序保证输出稳定。
func (s *ServerState) snapshotAdminData() adminDataFile {
	banned := make([]int, 0, len(s.BannedUsers))
	for id := range s.BannedUsers {
		banned = append(banned, id)
	}
	sort.Ints(banned)

	rooms := make(map[string][]int, len(s.BannedRoomUsers))
	for rid, set := range s.BannedRoomUsers {
		ids := make([]int, 0, len(set))
		for id := range set {
			ids = append(ids, id)
		}
		if len(ids) > 0 {
			sort.Ints(ids)
			rooms[string(rid)] = ids
		}
	}
	return adminDataFile{Version: 1, BannedUsers: banned, BannedRoomUsers: rooms}
}

// SaveAdminData 把封禁数据原子写回文件（写临时文件再 rename）。
func (s *ServerState) SaveAdminData() error {
	if s.AdminDataPath == "" {
		return nil
	}
	s.Mu.Lock()
	snap := s.snapshotAdminData()
	s.Mu.Unlock()
	return writeAdminDataFile(s.AdminDataPath, snap)
}

// FlushAdminDataNow 立即写入（用于关闭）。
func (s *ServerState) FlushAdminDataNow() error { return s.SaveAdminData() }

func writeAdminDataFile(path string, f adminDataFile) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(path) // Windows 文件锁退化：先删后改名
		return os.Rename(tmp, path)
	}
	return nil
}
