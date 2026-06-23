package httpapi

import (
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/server"
)

const roomListCacheTTL = 2 * time.Second

type roomListResponse struct {
	Rooms []roomEntry `json:"rooms"`
	Total int         `json:"total"`
}

type roomEntry struct {
	RoomID  string       `json:"roomid"`
	Cycle   bool         `json:"cycle"`
	Lock    bool         `json:"lock"`
	Host    idName       `json:"host"`
	State   string       `json:"state"`
	Chart   *chartInfo   `json:"chart"`
	Players []playerInfo `json:"players"`
}

type idName struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

type chartInfo struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

type playerInfo struct {
	Name string `json:"name"`
	ID   int    `json:"id"`
}

func roomStateString(st server.InternalRoomState) string {
	switch st.(type) {
	case server.StatePlaying:
		return "playing"
	case server.StateWaitForReady:
		return "waiting_for_ready"
	default:
		return "select_chart"
	}
}

// handleRoomList 返回公开房间列表（2s 缓存）。以 `_` 开头的房间 id 视为私有，过滤掉。
func (s *Service) handleRoomList(w http.ResponseWriter, _ *http.Request) {
	s.roomCacheMu.Lock()
	if s.roomCache != nil && time.Since(s.roomCacheAt) < roomListCacheTTL {
		cached := s.roomCache
		s.roomCacheMu.Unlock()
		s.writeRaw(w, cached)
		return
	}
	s.roomCacheMu.Unlock()

	resp := s.snapshotRooms()
	buf := s.encode(resp)

	s.roomCacheMu.Lock()
	s.roomCache = buf
	s.roomCacheAt = time.Now()
	s.roomCacheMu.Unlock()

	s.writeRaw(w, buf)
}

// snapshotRooms 在 state.Mu 下快照房间数据（不在锁内做序列化/IO）。
func (s *Service) snapshotRooms() roomListResponse {
	s.state.Mu.Lock()
	defer s.state.Mu.Unlock()

	resp := roomListResponse{Rooms: make([]roomEntry, 0, len(s.state.Rooms))}
	for id, room := range s.state.Rooms {
		roomid := string(id)
		if len(roomid) > 0 && roomid[0] == '_' {
			continue // 私有房间不公开
		}
		hostName := strconv.Itoa(room.HostID)
		if u := s.state.Users[room.HostID]; u != nil {
			hostName = u.Name
		}
		players := make([]playerInfo, 0, room.UserCount())
		for _, uid := range room.UserIDs() {
			name := strconv.Itoa(uid)
			if u := s.state.Users[uid]; u != nil {
				name = u.Name
			}
			players = append(players, playerInfo{Name: name, ID: uid})
		}
		resp.Total += len(players)

		var chart *chartInfo
		if room.Chart != nil {
			chart = &chartInfo{Name: room.Chart.Name, ID: strconv.Itoa(room.Chart.ID)}
		}
		resp.Rooms = append(resp.Rooms, roomEntry{
			RoomID:  roomid,
			Cycle:   room.Cycle,
			Lock:    room.Locked,
			Host:    idName{Name: hostName, ID: strconv.Itoa(room.HostID)},
			State:   roomStateString(room.State),
			Chart:   chart,
			Players: players,
		})
	}
	sort.Slice(resp.Rooms, func(i, j int) bool { return resp.Rooms[i].RoomID < resp.Rooms[j].RoomID })
	return resp
}
