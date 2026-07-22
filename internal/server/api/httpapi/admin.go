package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/common/platform/l10n"
	"github.com/Pimeng/gooophira-mp/internal/common/protocol"
	"github.com/Pimeng/gooophira-mp/internal/core/server"
)

const adminMaxFailedPerIP = 5

type attemptEntry struct {
	count int
	last  time.Time
}

// adminAuth 跟踪每 IP 的失败计数与封禁，对应 TS routes/auth.ts。
type adminAuth struct {
	mu       sync.Mutex
	failedIP map[string]*attemptEntry
	bannedIP map[string]time.Time
}

func newAdminAuth() *adminAuth {
	return &adminAuth{failedIP: make(map[string]*attemptEntry), bannedIP: make(map[string]time.Time)}
}

// extractAdminToken 按优先级取管理员 token：X-Admin-Token 头 > Authorization: Bearer >
// （仅当 allow_token_in_query 时）查询参数 token。对齐 TS httpService.extractAdminToken。
func (s *Service) extractAdminToken(r *http.Request) string {
	if h := strings.TrimSpace(r.Header.Get("X-Admin-Token")); h != "" {
		return h
	}
	if h := r.Header.Get("Authorization"); h != "" {
		if after, ok := strings.CutPrefix(h, "Bearer "); ok {
			return strings.TrimSpace(after)
		}
	}
	if s.state.Config.EffectiveAllowTokenInQuery() {
		return strings.TrimSpace(r.URL.Query().Get("token"))
	}
	return ""
}

// checkAdmin 校验管理员鉴权；未通过时已写出错误响应并返回 false。
func (s *Service) checkAdmin(w http.ResponseWriter, r *http.Request, ip string, lang *l10n.Language) bool {
	reqToken := s.extractAdminToken(r)

	s.auth.mu.Lock()
	if _, banned := s.auth.bannedIP[ip]; banned {
		s.auth.mu.Unlock()
		s.adminErr(w, lang, http.StatusUnauthorized, "unauthorized", "auth-unauthorized")
		return false
	}
	s.auth.mu.Unlock()

	// 临时 token（IP 绑定 + 过期），对应 state.TempAdminTokens。
	if reqToken != "" {
		s.state.Mu.Lock()
		tok := s.state.TempAdminTokens[reqToken]
		s.state.Mu.Unlock()
		if tok != nil {
			if tok.Banned || time.Now().UnixMilli() > tok.ExpiresAt {
				s.adminErr(w, lang, http.StatusUnauthorized, "token-expired", "token-expired")
				return false
			}
			if tok.IP != ip {
				tok.Banned = true // IP 不符：封禁该 token（不显式告知）
				s.adminErr(w, lang, http.StatusUnauthorized, "token-expired", "token-expired")
				return false
			}
			return true
		}
	}

	// 永久 admin token。
	adminToken := strings.TrimSpace(strOrEmpty(s.state.Config.AdminToken))
	if adminToken == "" {
		s.adminErr(w, lang, http.StatusForbidden, "admin-disabled", "admin-disabled")
		return false
	}
	if reqToken == "" || reqToken != adminToken {
		s.recordFailure(ip)
		s.adminErr(w, lang, http.StatusUnauthorized, "unauthorized", "auth-unauthorized")
		return false
	}
	s.auth.mu.Lock()
	delete(s.auth.failedIP, ip)
	s.auth.mu.Unlock()
	return true
}

func (s *Service) recordFailure(ip string) {
	s.auth.mu.Lock()
	defer s.auth.mu.Unlock()
	e := s.auth.failedIP[ip]
	if e == nil {
		e = &attemptEntry{}
		s.auth.failedIP[ip] = e
	}
	e.count++
	e.last = time.Now()
	if e.count >= adminMaxFailedPerIP {
		s.auth.bannedIP[ip] = time.Now()
	}
}

func (s *Service) adminErr(w http.ResponseWriter, lang *l10n.Language, status int, code, msgKey string) {
	s.writeJSON(w, status, map[string]any{"ok": false, "error": code, "message": l10n.TL(lang, msgKey, nil)})
}

// routeAdmin 分发管理路由；返回 false 表示未匹配。
func (s *Service) routeAdmin(w http.ResponseWriter, r *http.Request, lang *l10n.Language) bool {
	if s.routeAdminFeishu(w, r, lang) {
		return true
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/admin/rooms":
		s.state.Mu.Lock()
		rooms := s.state.BuildAdminRooms()
		s.state.Mu.Unlock()
		s.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "rooms": rooms})
	case r.Method == http.MethodGet && r.URL.Path == "/admin/users":
		s.state.Mu.Lock()
		users := s.state.BuildOnlineUsers()
		s.state.Mu.Unlock()
		s.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "users": users})
	case r.Method == http.MethodPost && r.URL.Path == "/admin/broadcast":
		s.handleAdminBroadcast(w, r, lang)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/disband":
		s.handleAdminDisband(w, r, lang)
	case r.Method == http.MethodGet && r.URL.Path == "/admin/metrics":
		s.handleAdminMetrics(w, r, lang)
	default:
		return s.routeAdminUsers(w, r, lang) || s.routeAdminContest(w, r) ||
			s.routeAdminConsole(w, r, lang) || s.routeAdminConfig(w, r, lang)
	}
	return true
}

func (s *Service) handleAdminBroadcast(w http.ResponseWriter, r *http.Request, lang *l10n.Language) {
	var body struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Message) == "" {
		s.adminErr(w, lang, http.StatusBadRequest, "bad-request", "bad-request")
		return
	}
	s.state.Mu.Lock()
	rooms := make([]*server.Room, 0, len(s.state.Rooms))
	for _, room := range s.state.Rooms {
		rooms = append(rooms, room)
	}
	s.state.Mu.Unlock()
	sysID := s.state.SystemChatUserID()
	for _, room := range rooms {
		room.Mu.Lock()
		s.hub.BroadcastRoomMessage(room, protocol.MsgChat{User: sysID, Content: body.Message})
		room.Mu.Unlock()
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "rooms": len(rooms)})
}

func (s *Service) handleAdminDisband(w http.ResponseWriter, r *http.Request, lang *l10n.Language) {
	var body struct {
		RoomID string `json:"roomid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.RoomID) == "" {
		s.adminErr(w, lang, http.StatusBadRequest, "bad-request", "bad-request")
		return
	}
	s.state.Mu.Lock()
	room := s.state.Rooms[protocol.RoomID(body.RoomID)]
	if room != nil {
		s.hub.DisbandRoom(room)
	}
	s.state.Mu.Unlock()
	if room == nil {
		s.adminErr(w, lang, http.StatusNotFound, "room-not-found", "room-not-found")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func strOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// verifyAdminToken 仅校验 token 有效性（不写响应、不计失败），用于 WebSocket admin 订阅。
func (s *Service) verifyAdminToken(token, ip string) bool {
	if token == "" {
		return false
	}
	s.state.Mu.Lock()
	tok := s.state.TempAdminTokens[token]
	s.state.Mu.Unlock()
	if tok != nil && !tok.Banned && time.Now().UnixMilli() <= tok.ExpiresAt && tok.IP == ip {
		return true
	}
	adminToken := strings.TrimSpace(strOrEmpty(s.state.Config.AdminToken))
	return adminToken != "" && token == adminToken
}
