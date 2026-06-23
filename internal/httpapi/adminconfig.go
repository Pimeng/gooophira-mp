package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/l10n"
)

// 管理员配置类路由（对应 TS routes/adminConfigRoutes.ts）：
//   - GET/POST /admin/runtime-config        运行时可热更新配置的查询与批量改动
//   - POST     /admin/runtime-config/rollback 撤销上一次运行时配置改动
//   - GET/POST /admin/replay/config          回放开关
//   - GET/POST /admin/room-creation/config   建房开关
//
// 改动经 state.ApplyRuntimePatch 落盘（保留注释）并热生效，同时记录回滚快照。

// routeAdminConfig 分发配置类管理路由；返回 false 表示未匹配。
func (s *Service) routeAdminConfig(w http.ResponseWriter, r *http.Request, lang *l10n.Language) bool {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/admin/runtime-config":
		s.handleRuntimeConfigGet(w)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/runtime-config":
		s.handleRuntimeConfigPost(w, r, lang)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/runtime-config/rollback":
		s.handleRuntimeConfigRollback(w, lang)
	case r.Method == http.MethodGet && r.URL.Path == "/admin/replay/config":
		s.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": s.replayEnabled()})
	case r.Method == http.MethodPost && r.URL.Path == "/admin/replay/config":
		s.handleToggleConfigPost(w, r, lang, "REPLAY_ENABLED", s.replayEnabled)
	case r.Method == http.MethodGet && r.URL.Path == "/admin/room-creation/config":
		s.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": s.roomCreationEnabled()})
	case r.Method == http.MethodPost && r.URL.Path == "/admin/room-creation/config":
		s.handleToggleConfigPost(w, r, lang, "ROOM_CREATION_ENABLED", s.roomCreationEnabled)
	default:
		return false
	}
	return true
}

func (s *Service) replayEnabled() bool {
	s.state.Mu.Lock()
	defer s.state.Mu.Unlock()
	return s.state.ReplayEnabled
}

func (s *Service) roomCreationEnabled() bool {
	s.state.Mu.Lock()
	defer s.state.Mu.Unlock()
	return s.state.RoomCreationEnabled
}

func (s *Service) handleRuntimeConfigGet(w http.ResponseWriter) {
	s.state.Mu.Lock()
	snapshot := config.BuildRuntimeConfigSnapshot(s.state.Config)
	rollbackAvailable := s.state.LastRuntimeConfigRollback != nil
	s.state.Mu.Unlock()
	s.writeJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"managedKeys":       config.RuntimeConfigEnvNames(),
		"rollbackAvailable": rollbackAvailable,
		"config":            snapshot,
	})
}

func (s *Service) handleRuntimeConfigPost(w http.ResponseWriter, r *http.Request, lang *l10n.Language) {
	body, ok := decodeJSONObject(r)
	if !ok {
		s.writeRuntimeConfigError(w, lang, config.RuntimePatchResult{Empty: true})
		return
	}
	res := config.ParseRuntimeConfigPatch(body)
	if !res.OK {
		s.writeRuntimeConfigError(w, lang, res)
		return
	}
	s.state.ApplyRuntimePatch(res)
	s.writeRuntimeConfigSuccess(w, res.Keys)
}

func (s *Service) handleRuntimeConfigRollback(w http.ResponseWriter, lang *l10n.Language) {
	s.state.Mu.Lock()
	rollback := s.state.LastRuntimeConfigRollback
	s.state.Mu.Unlock()
	if rollback == nil {
		s.adminErr(w, lang, http.StatusConflict, "runtime-config-rollback-unavailable", "bad-request")
		return
	}
	res := config.ParseRuntimeConfigPatch(rollback)
	if !res.OK {
		s.adminErr(w, lang, http.StatusInternalServerError, "runtime-config-rollback-invalid", "http-internal-error")
		return
	}
	// ApplyRuntimePatch 会把 LastRuntimeConfigRollback 更新为本次回滚前的值（即可再次撤销）。
	s.state.ApplyRuntimePatch(res)
	s.writeRuntimeConfigSuccess(w, res.Keys)
}

// handleToggleConfigPost 处理只含单个布尔开关的 POST（replay/room-creation）。
func (s *Service) handleToggleConfigPost(w http.ResponseWriter, r *http.Request, lang *l10n.Language, env string, currentEnabled func() bool) {
	body, ok := decodeJSONObject(r)
	if !ok {
		s.adminErr(w, lang, http.StatusBadRequest, "bad-enabled", "bad-enabled")
		return
	}
	raw, present := body["enabled"]
	if !present {
		s.adminErr(w, lang, http.StatusBadRequest, "bad-enabled", "bad-enabled")
		return
	}
	res := config.ParseRuntimeConfigPatch(map[string]any{env: raw})
	if !res.OK {
		s.adminErr(w, lang, http.StatusBadRequest, "bad-enabled", "bad-enabled")
		return
	}
	s.state.ApplyRuntimePatch(res)
	if s.state.Logger != nil {
		s.state.Logger.Info("Runtime config persisted: " + env)
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": currentEnabled()})
}

func (s *Service) writeRuntimeConfigSuccess(w http.ResponseWriter, updatedKeys []string) {
	s.state.Mu.Lock()
	snapshot := config.BuildRuntimeConfigSnapshot(s.state.Config)
	rollbackAvailable := s.state.LastRuntimeConfigRollback != nil
	s.state.Mu.Unlock()
	s.writeJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"updatedKeys":       updatedKeys,
		"rollbackAvailable": rollbackAvailable,
		"config":            snapshot,
	})
}

func (s *Service) writeRuntimeConfigError(w http.ResponseWriter, lang *l10n.Language, res config.RuntimePatchResult) {
	s.writeJSON(w, http.StatusBadRequest, map[string]any{
		"ok":              false,
		"error":           "bad-runtime-config",
		"message":         l10n.TL(lang, "bad-request", nil),
		"invalidKeys":     orEmpty(res.InvalidKeys),
		"startupOnlyKeys": orEmpty(res.StartupOnlyKeys),
		"unsupportedKeys": orEmpty(res.UnsupportedKeys),
		"managedKeys":     config.RuntimeConfigEnvNames(),
	})
}

// decodeJSONObject 把请求体解析为 JSON 对象；非对象/解析失败返回 ok=false。
func decodeJSONObject(r *http.Request) (map[string]any, bool) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body == nil {
		return nil, false
	}
	return body, true
}

// orEmpty 把 nil 切片归一化为空切片，确保 JSON 序列化为 [] 而非 null。
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
