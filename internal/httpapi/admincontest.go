package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/Pimeng/gooophira-mp/internal/protocol"
)

// 管理员比赛模式路由（对应 TS routes/adminContestRoutes.ts）：
//   - POST /admin/contest/rooms/:id/config      启用/停用比赛模式（可带白名单）
//   - POST /admin/contest/rooms/:id/whitelist   替换白名单
//   - POST /admin/contest/rooms/:id/start        强制开赛（force 可选）

// routeAdminContest 分发比赛模式路由；返回 false 表示未匹配。
func (s *Service) routeAdminContest(w http.ResponseWriter, r *http.Request) bool {
	rest, ok := strings.CutPrefix(r.URL.Path, "/admin/contest/rooms/")
	if !ok || r.Method != http.MethodPost {
		return false
	}
	idx := strings.LastIndex(rest, "/")
	if idx <= 0 {
		return false
	}
	idEnc, action := rest[:idx], rest[idx+1:]
	roomID, err := url.PathUnescape(idEnc)
	if err != nil || roomID == "" {
		return false
	}
	rid := protocol.RoomID(roomID)
	switch action {
	case "config":
		s.handleContestConfig(w, r, rid)
	case "whitelist":
		s.handleContestWhitelist(w, r, rid)
	case "start":
		s.handleContestStart(w, r, rid)
	default:
		return false
	}
	return true
}

func (s *Service) handleContestConfig(w http.ResponseWriter, r *http.Request, rid protocol.RoomID) {
	body, _ := decodeJSONObject(r)
	enabled := true
	if v, present := body["enabled"]; present {
		enabled = jsonBool(v)
	}
	whitelist, _ := jsonIntSlice(body["whitelist"])

	s.state.Mu.Lock()
	room := s.state.Rooms[rid]
	if room != nil {
		if enabled {
			s.hub.EnableContest(room, whitelist)
		} else {
			s.hub.DisableContest(room)
		}
	}
	s.state.Mu.Unlock()
	if room == nil {
		s.writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "room-not-found"})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Service) handleContestWhitelist(w http.ResponseWriter, r *http.Request, rid protocol.RoomID) {
	body, _ := decodeJSONObject(r)
	userIDs, ok := jsonIntSlice(body["userIds"])
	if !ok {
		s.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad-user-ids"})
		return
	}
	s.state.Mu.Lock()
	room := s.state.Rooms[rid]
	applied := room != nil && s.hub.SetContestWhitelist(room, userIDs)
	s.state.Mu.Unlock()
	if !applied {
		s.writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "contest-room-not-found"})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Service) handleContestStart(w http.ResponseWriter, r *http.Request, rid protocol.RoomID) {
	body, _ := decodeJSONObject(r)
	force := jsonBool(body["force"])

	s.state.Mu.Lock()
	room := s.state.Rooms[rid]
	var startErr error
	if room == nil {
		startErr = errRoomMissing
	} else {
		startErr = s.hub.StartContest(room, force)
	}
	s.state.Mu.Unlock()

	if startErr != nil {
		status := http.StatusBadRequest
		code := startErr.Error()
		if errors.Is(startErr, errRoomMissing) {
			status, code = http.StatusNotFound, "contest-room-not-found"
		} else if code == "contest-room-not-found" {
			status = http.StatusNotFound
		}
		s.writeJSON(w, status, map[string]any{"ok": false, "error": code})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

var errRoomMissing = errors.New("room-missing")

// jsonIntSlice 把 JSON 数组转为 []int（非数组返回 ok=false；非整数项跳过）。
func jsonIntSlice(v any) ([]int, bool) {
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]int, 0, len(arr))
	for _, it := range arr {
		if n, ok := jsonInt(it); ok {
			out = append(out, n)
		}
	}
	return out, true
}
