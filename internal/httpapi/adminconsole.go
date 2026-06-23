package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Pimeng/gooophira-mp/internal/l10n"
)

// GUI 控制台路由（对应 TS routes/adminConsoleRoutes.ts）：
//   - GET  /admin/console/logs?limit=N   最近的控制台日志（WS 不可用时的回填/轮询）
//   - POST /admin/console/command        执行一条 CLI 命令并返回捕获的输出

const consoleMaxCommandLen = 500

// routeAdminConsole 分发 GUI 控制台路由；返回 false 表示未匹配。
func (s *Service) routeAdminConsole(w http.ResponseWriter, r *http.Request, _ *l10n.Language) bool {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/admin/console/logs":
		limit := 200
		if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
			limit = min(n, 500)
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "lines": s.state.ConsoleHub.GetRecent(limit)})
		return true
	case r.Method == http.MethodPost && r.URL.Path == "/admin/console/command":
		s.handleConsoleCommand(w, r)
		return true
	}
	return false
}

func (s *Service) handleConsoleCommand(w http.ResponseWriter, r *http.Request) {
	body, _ := decodeJSONObject(r)
	command := strings.TrimSpace(jsonString(body["command"]))
	if command == "" {
		s.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad-command"})
		return
	}
	if len(command) > consoleMaxCommandLen {
		s.writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "command-too-long"})
		return
	}
	exec := s.state.ConsoleExecutor
	if exec == nil {
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "console-not-ready"})
		return
	}
	lines, err := exec(command)
	if err != nil {
		s.writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "command-failed", "message": err.Error()})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "lines": lines})
}
