package httpapi

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/agentipc"
	"github.com/Pimeng/gooophira-mp/internal/agentproto"
	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"github.com/Pimeng/gooophira-mp/internal/protocol"
	"github.com/Pimeng/gooophira-mp/internal/replay"
)

// 玩家可见的回放路由（对应 TS routes/replayPublicRoutes.ts，去除依赖分享站的手动上传）：
//   - POST /replay/auth                 用 Phira token 认证并列出本人回放，签发下载会话
//   - GET  /replay/download             凭会话下载某份回放
//   - POST /replay/delete               凭会话删除某份回放
//   - GET/POST /replay/auto-upload/config  本人自动上传显示开关
//
// 注：POST /replay/upload（上传到分享站）依赖 autoUpload 子系统，后续接入。

const replaySessionTTL = 30 * time.Minute

type replaySession struct {
	userID    int
	expiresAt int64 // unix 毫秒
}

// routeReplay 分发玩家回放路由；返回 false 表示未匹配。
func (s *Service) routeReplay(w http.ResponseWriter, r *http.Request, lang *l10n.Language) bool {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/replay/auth":
		s.handleReplayAuth(w, r, lang)
	case r.Method == http.MethodGet && r.URL.Path == "/replay/download":
		s.handleReplayDownload(w, r, lang)
	case r.Method == http.MethodPost && r.URL.Path == "/replay/delete":
		s.handleReplayDelete(w, r, lang)
	case r.Method == http.MethodPost && r.URL.Path == "/replay/upload":
		s.handleReplayUpload(w, r, lang)
	case r.URL.Path == "/replay/auto-upload/config":
		s.handleAutoUploadConfig(w, r, lang)
	default:
		return false
	}
	return true
}

// verifyUserToken 用 Phira /me 验证用户 token，返回用户 ID。
func (s *Service) verifyUserToken(token string) (int, bool) {
	if token == "" || s.hub == nil || s.hub.Phira == nil {
		return 0, false
	}
	info, err := s.hub.FetchUserInfo(token)
	if err != nil || info.ID <= 0 {
		return 0, false
	}
	return info.ID, true
}

func (s *Service) replayBaseDir() string { return s.state.Config.EffectiveReplayBaseDir() }

// createReplaySession 生成一个绑定用户的下载会话 token。
func (s *Service) createReplaySession(userID int) (string, int64) {
	token := protocol.NewUUID()
	expiresAt := time.Now().Add(replaySessionTTL).UnixMilli()
	s.replayMu.Lock()
	s.pruneReplaySessionsLocked()
	s.replaySessions[token] = replaySession{userID: userID, expiresAt: expiresAt}
	s.replayMu.Unlock()
	return token, expiresAt
}

// getReplaySession 取并校验下载会话（过期视为无效）。
func (s *Service) getReplaySession(token string) (replaySession, bool) {
	s.replayMu.Lock()
	defer s.replayMu.Unlock()
	sess, ok := s.replaySessions[token]
	if !ok || time.Now().UnixMilli() > sess.expiresAt {
		return replaySession{}, false
	}
	return sess, true
}

func (s *Service) pruneReplaySessionsLocked() {
	now := time.Now().UnixMilli()
	for k, v := range s.replaySessions {
		if now > v.expiresAt {
			delete(s.replaySessions, k)
		}
	}
}

func (s *Service) handleReplayAuth(w http.ResponseWriter, r *http.Request, lang *l10n.Language) {
	body, _ := decodeJSONObject(r)
	token := strings.TrimSpace(jsonString(body["token"]))
	if token == "" {
		s.adminErr(w, lang, http.StatusBadRequest, "bad-token", "bad-token")
		return
	}
	userID, ok := s.verifyUserToken(token)
	if !ok {
		s.adminErr(w, lang, http.StatusUnauthorized, "unauthorized", "auth-unauthorized")
		return
	}

	listed := replay.ListReplaysForUser(s.replayBaseDir(), userID)
	chartIDs := make([]int, 0, len(listed))
	for id := range listed {
		chartIDs = append(chartIDs, id)
	}
	sort.Ints(chartIDs)
	charts := make([]map[string]any, 0, len(chartIDs))
	for _, cid := range chartIDs {
		replays := make([]map[string]any, 0, len(listed[cid]))
		for _, e := range listed[cid] {
			replays = append(replays, map[string]any{"timestamp": e.Timestamp, "recordId": e.RecordID})
		}
		charts = append(charts, map[string]any{"chartId": cid, "replays": replays})
	}

	token2, expiresAt := s.createReplaySession(userID)
	s.writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "userId": userID, "charts": charts,
		"sessionToken": token2, "expiresAt": expiresAt,
	})
}

func (s *Service) handleReplayDownload(w http.ResponseWriter, r *http.Request, lang *l10n.Language) {
	q := r.URL.Query()
	sessionToken := strings.TrimSpace(q.Get("sessionToken"))
	chartID, e1 := strconv.Atoi(q.Get("chartId"))
	timestamp, e2 := strconv.ParseInt(q.Get("timestamp"), 10, 64)
	if sessionToken == "" || e1 != nil || e2 != nil || chartID < 0 || timestamp <= 0 {
		s.adminErr(w, lang, http.StatusBadRequest, "bad-request", "bad-request")
		return
	}
	sess, ok := s.getReplaySession(sessionToken)
	if !ok {
		s.adminErr(w, lang, http.StatusUnauthorized, "unauthorized", "auth-unauthorized")
		return
	}

	path := replay.FilePath(s.replayBaseDir(), sess.userID, chartID, timestamp)
	data, err := os.ReadFile(path)
	if err != nil {
		s.adminErr(w, lang, http.StatusNotFound, "not-found", "http-not-found")
		return
	}
	// 防御性校验：文件头里的归属须与会话用户/谱面一致。
	if h, herr := replay.ReadReplayHeader(path); herr != nil || h.UserID != sess.userID || h.ChartID != chartID {
		s.adminErr(w, lang, http.StatusNotFound, "not-found", "http-not-found")
		return
	}

	w.Header().Set("content-type", "application/octet-stream")
	w.Header().Set("cache-control", "no-store")
	w.Header().Set("content-disposition", "attachment; filename=\""+strconv.FormatInt(timestamp, 10)+".phirarec\"")
	w.Header().Set("content-length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

func (s *Service) handleReplayDelete(w http.ResponseWriter, r *http.Request, lang *l10n.Language) {
	body, _ := decodeJSONObject(r)
	sessionToken := strings.TrimSpace(jsonString(body["sessionToken"]))
	chartID, okC := jsonInt(body["chartId"])
	timestamp, okT := jsonInt64(body["timestamp"])
	if sessionToken == "" || !okC || !okT || chartID < 0 || timestamp <= 0 {
		s.adminErr(w, lang, http.StatusBadRequest, "bad-request", "bad-request")
		return
	}
	sess, ok := s.getReplaySession(sessionToken)
	if !ok {
		s.adminErr(w, lang, http.StatusUnauthorized, "unauthorized", "auth-unauthorized")
		return
	}
	// 仅当文件归属与会话用户一致才删除。
	path := replay.FilePath(s.replayBaseDir(), sess.userID, chartID, timestamp)
	if h, herr := replay.ReadReplayHeader(path); herr != nil || h.UserID != sess.userID || h.ChartID != chartID {
		s.adminErr(w, lang, http.StatusNotFound, "not-found", "http-not-found")
		return
	}
	deleted, err := replay.DeleteReplayForUser(s.replayBaseDir(), sess.userID, chartID, timestamp)
	if err != nil || !deleted {
		s.adminErr(w, lang, http.StatusNotFound, "not-found", "http-not-found")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleReplayUpload validates player ownership and delegates the upload to Agent.
func (s *Service) handleReplayUpload(w http.ResponseWriter, r *http.Request, lang *l10n.Language) {
	body, _ := decodeJSONObject(r)
	token := strings.TrimSpace(jsonString(body["token"]))
	chartID, okC := jsonInt(body["chartId"])
	timestamp, okT := jsonInt64(body["timestamp"])
	if token == "" || !okC || !okT || chartID < 0 || timestamp <= 0 {
		s.adminErr(w, lang, http.StatusBadRequest, "bad-request", "bad-request")
		return
	}
	userID, ok := s.verifyUserToken(token)
	if !ok {
		s.adminErr(w, lang, http.StatusUnauthorized, "unauthorized", "auth-unauthorized")
		return
	}
	replayID := (replay.ID{UserID: userID, ChartID: chartID, Timestamp: timestamp}).String()
	s.queryReplayAgent(w, r, lang, agentproto.QueryReplayUpload, agentproto.ReplayUploadParams{ReplayID: replayID, Visible: true})
}

func (s *Service) handleAutoUploadConfig(w http.ResponseWriter, r *http.Request, lang *l10n.Language) {
	var token string
	var setShow *bool
	switch r.Method {
	case http.MethodGet:
		token = strings.TrimSpace(r.URL.Query().Get("token"))
	case http.MethodPost:
		body, _ := decodeJSONObject(r)
		token = strings.TrimSpace(jsonString(body["token"]))
		if v, ok := body["show"].(bool); ok {
			setShow = &v
		}
	default:
		s.adminErr(w, lang, http.StatusBadRequest, "bad-request", "bad-request")
		return
	}
	if token == "" {
		s.adminErr(w, lang, http.StatusBadRequest, "bad-token", "bad-token")
		return
	}
	userID, ok := s.verifyUserToken(token)
	if !ok {
		s.adminErr(w, lang, http.StatusUnauthorized, "unauthorized", "auth-unauthorized")
		return
	}

	s.queryReplayAgent(w, r, lang, agentproto.QueryReplayAutoConfig, agentproto.ReplayAutoConfigParams{UserID: userID, Show: setShow})
}

func (s *Service) queryReplayAgent(w http.ResponseWriter, r *http.Request, lang *l10n.Language, method string, params any) {
	if s.agent == nil {
		s.adminErr(w, lang, http.StatusServiceUnavailable, "agent-unavailable", "service-unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 65*time.Second)
	defer cancel()
	response, err := s.agent.Query(ctx, method, params)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, agentipc.ErrAgentUnavailable) {
			status = http.StatusServiceUnavailable
		}
		s.adminErr(w, lang, status, "agent-unavailable", "service-unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(response.Body)
}

// jsonInt64 把 JSON 值（float64/int）转为 int64。
func jsonInt64(v any) (int64, bool) {
	switch x := v.(type) {
	case float64:
		if x != float64(int64(x)) {
			return 0, false
		}
		return int64(x), true
	case int:
		return int64(x), true
	default:
		return 0, false
	}
}
