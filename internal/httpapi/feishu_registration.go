package httpapi

import (
	"context"
	"encoding/json"
	"github.com/Pimeng/gooophira-mp/internal/agentproto"
	"github.com/Pimeng/gooophira-mp/internal/l10n"
	"net/http"
	"strings"
	"time"
)

func (s *Service) routeAdminFeishu(w http.ResponseWriter, r *http.Request, lang *l10n.Language) bool {
	const prefix = "/admin/feishu/app-registration"
	if r.URL.Path != prefix && !strings.HasPrefix(r.URL.Path, prefix+"/") {
		return false
	}
	if s.agent == nil {
		s.writeJSON(w, 503, map[string]any{"ok": false, "error": "agent-unavailable"})
		return true
	}
	var p agentproto.FeishuAppRegistrationParams
	if r.Method == http.MethodPost && r.URL.Path == prefix {
		if json.NewDecoder(r.Body).Decode(&p) != nil {
			s.writeJSON(w, 400, map[string]any{"ok": false, "error": "invalid-request"})
			return true
		}
		p.Action = "start"
	} else {
		tid := strings.TrimPrefix(r.URL.Path, prefix+"/")
		p.TaskID = tid
		if r.Method == http.MethodGet {
			p.Action = "status"
		} else if r.Method == http.MethodDelete {
			p.Action = "cancel"
		} else {
			return false
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 65*time.Second)
	defer cancel()
	resp, err := s.agent.Query(ctx, agentproto.QueryFeishuAppRegistration, p)
	if err != nil {
		s.writeJSON(w, 503, map[string]any{"ok": false, "error": "agent-unavailable"})
		return true
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(resp.Body)
	return true
}
