package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
	"github.com/Pimeng/gooophira-mp/internal/server"
)

// 管理员用户管理路由（对应 TS routes/adminUserRoutes.ts）：
//   - GET  /admin/users/:id              单个在线用户信息
//   - POST /admin/users/:id/disconnect   断开（踢下线）
//   - POST /admin/ban/user               全局封禁/解封（可选断开）
//   - POST /admin/ban/room               房间级封禁/解封
//
// 复用与 CLI 相同的 state 操作（BannedUsers / BannedRoomUsers + SaveAdminData + Session.Close）。

// routeAdminUsers 分发用户管理路由；返回 false 表示未匹配。
func (s *Service) routeAdminUsers(w http.ResponseWriter, r *http.Request, lang *l10n.Language) bool {
	path := r.URL.Path
	switch {
	case r.Method == http.MethodPost && path == "/admin/ban/user":
		s.handleAdminBanUser(w, r, lang)
		return true
	case r.Method == http.MethodPost && path == "/admin/ban/room":
		s.handleAdminBanRoom(w, r, lang)
		return true
	}
	if rest, ok := strings.CutPrefix(path, "/admin/users/"); ok {
		if idStr, isDisc := strings.CutSuffix(rest, "/disconnect"); isDisc {
			if r.Method == http.MethodPost {
				s.handleAdminDisconnect(w, lang, idStr)
				return true
			}
		} else if idStr, isMove := strings.CutSuffix(rest, "/move"); isMove {
			if r.Method == http.MethodPost {
				s.handleAdminMove(w, r, lang, idStr)
				return true
			}
		} else if r.Method == http.MethodGet && !strings.Contains(rest, "/") {
			s.handleAdminUserGet(w, lang, rest)
			return true
		}
	}
	return false
}

// userView 是单个用户的管理视图（字段名对齐 TS）。Session 由 u.IsConnected
// 内部加锁读取；调用方仅需持 state.Mu 保护 u / banned 的快照。
func userView(u *server.User, banned bool) map[string]any {
	room := any(nil)
	if u.Room != nil {
		room = string(u.Room.ID)
	}
	return map[string]any{
		"id":        u.ID,
		"name":      u.Name,
		"monitor":   u.Monitor,
		"connected": u.IsConnected(),
		"room":      room,
		"banned":    banned,
	}
}

func (s *Service) handleAdminUserGet(w http.ResponseWriter, lang *l10n.Language, idStr string) {
	id, err := strconv.Atoi(idStr)
	if err != nil {
		s.adminErr(w, lang, http.StatusBadRequest, "bad-user-id", "cli-bad-user-id")
		return
	}
	s.state.Mu.Lock()
	u := s.state.Users[id]
	var view map[string]any
	if u != nil {
		_, banned := s.state.BannedUsers[id]
		view = userView(u, banned)
	}
	s.state.Mu.Unlock()
	if u == nil {
		s.writeJSON(w, http.StatusNotFound, map[string]any{
			"ok": false, "error": "user-not-found",
			"message": l10n.TL(lang, "cli-user-not-found", map[string]string{"id": idStr}),
		})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": view})
}

func (s *Service) handleAdminBanUser(w http.ResponseWriter, r *http.Request, lang *l10n.Language) {
	body, ok := decodeJSONObject(r)
	if !ok {
		s.adminErr(w, lang, http.StatusBadRequest, "bad-user-id", "cli-bad-user-id")
		return
	}
	id, ok := jsonInt(body["userId"])
	if !ok {
		s.adminErr(w, lang, http.StatusBadRequest, "bad-user-id", "cli-bad-user-id")
		return
	}
	banned := jsonBool(body["banned"])
	disconnect := jsonBool(body["disconnect"])

	s.state.Mu.Lock()
	if banned {
		s.state.BannedUsers[id] = struct{}{}
	} else {
		delete(s.state.BannedUsers, id)
	}
	var sess server.Session
	if disconnect {
		if u := s.state.Users[id]; u != nil {
			sess = u.Session()
		}
	}
	s.state.Mu.Unlock()
	_ = s.state.SaveAdminData()
	if sess != nil {
		sess.Close() // 锁外关闭；被封禁用户的 cleanup 会立即退房移除
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Service) handleAdminBanRoom(w http.ResponseWriter, r *http.Request, lang *l10n.Language) {
	body, ok := decodeJSONObject(r)
	if !ok {
		s.adminErr(w, lang, http.StatusBadRequest, "bad-user-id", "cli-bad-user-id")
		return
	}
	id, ok := jsonInt(body["userId"])
	if !ok {
		s.adminErr(w, lang, http.StatusBadRequest, "bad-user-id", "cli-bad-user-id")
		return
	}
	roomID := strings.TrimSpace(jsonString(body["roomId"]))
	if roomID == "" {
		s.adminErr(w, lang, http.StatusBadRequest, "bad-request", "bad-request")
		return
	}
	banned := jsonBool(body["banned"])
	rid := protocol.RoomID(roomID)

	s.state.Mu.Lock()
	set := s.state.BannedRoomUsers[rid]
	if banned {
		if set == nil {
			set = make(map[int]struct{})
			s.state.BannedRoomUsers[rid] = set
		}
		set[id] = struct{}{}
	} else if set != nil {
		delete(set, id)
		if len(set) == 0 {
			delete(s.state.BannedRoomUsers, rid)
		}
	}
	s.state.Mu.Unlock()
	_ = s.state.SaveAdminData()
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAdminMove 把一名离线用户迁移到另一空闲房间。
func (s *Service) handleAdminMove(w http.ResponseWriter, r *http.Request, lang *l10n.Language, idStr string) {
	id, err := strconv.Atoi(idStr)
	if err != nil {
		s.adminErr(w, lang, http.StatusBadRequest, "bad-user-id", "cli-bad-user-id")
		return
	}
	body, _ := decodeJSONObject(r)
	roomID := strings.TrimSpace(jsonString(body["roomId"]))
	if roomID == "" {
		s.adminErr(w, lang, http.StatusBadRequest, "bad-request", "bad-request")
		return
	}
	monitor := jsonBool(body["monitor"])

	s.state.Mu.Lock()
	user := s.state.Users[id]
	toRoom := s.state.Rooms[protocol.RoomID(roomID)]
	var moveErr error
	switch {
	case user == nil:
		moveErr = errMoveUserNotFound
	case toRoom == nil:
		moveErr = errMoveRoomNotFound
	default:
		moveErr = s.hub.MoveUser(user, toRoom, monitor)
	}
	s.state.Mu.Unlock()

	if moveErr != nil {
		status, code := moveStatus(moveErr)
		s.writeJSON(w, status, map[string]any{"ok": false, "error": code})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

var (
	errMoveUserNotFound = errSentinel("user-not-found")
	errMoveRoomNotFound = errSentinel("room-not-found")
)

type errSentinel string

func (e errSentinel) Error() string { return string(e) }

// moveStatus 把迁移错误映射为 HTTP 状态码与错误码。
func moveStatus(err error) (int, string) {
	code := err.Error()
	switch code {
	case "user-not-found", "room-not-found":
		return http.StatusNotFound, code
	default:
		return http.StatusBadRequest, code
	}
}

func (s *Service) handleAdminDisconnect(w http.ResponseWriter, lang *l10n.Language, idStr string) {
	id, err := strconv.Atoi(idStr)
	if err != nil {
		s.adminErr(w, lang, http.StatusBadRequest, "bad-user-id", "cli-bad-user-id")
		return
	}
	s.state.Mu.Lock()
	var sess server.Session
	if u := s.state.Users[id]; u != nil {
		sess = u.Session()
	}
	s.state.Mu.Unlock()
	if sess == nil {
		s.writeJSON(w, http.StatusNotFound, map[string]any{
			"ok": false, "error": "user-not-connected",
			"message": l10n.TL(lang, "cli-user-not-connected", map[string]string{"id": idStr}),
		})
		return
	}
	sess.Close()
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------- JSON 值强转辅助（请求体来自 json.Unmarshal，数字为 float64） ----------

func jsonInt(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		if x != float64(int(x)) { // 必须是整数
			return 0, false
		}
		return int(x), true
	case int:
		return x, true
	default:
		return 0, false
	}
}

func jsonBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func jsonString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
