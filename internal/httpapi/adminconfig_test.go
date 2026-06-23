package httpapi

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/Pimeng/gooophira-mp/internal/config"
)

func adminCfg() *config.ServerConfig { return &config.ServerConfig{AdminToken: sp("secret")} }

func TestAdminConfig_RuntimeGet(t *testing.T) {
	svc, _ := newTestService(t, adminCfg())
	w := doAuth(svc, http.MethodGet, "/admin/runtime-config", "secret", "")
	if w.Code != 200 {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		OK                bool           `json:"ok"`
		ManagedKeys       []string       `json:"managedKeys"`
		RollbackAvailable bool           `json:"rollbackAvailable"`
		Config            map[string]any `json:"config"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK || len(resp.ManagedKeys) == 0 {
		t.Fatalf("unexpected response: %s", w.Body.String())
	}
	if resp.RollbackAvailable {
		t.Error("no change yet → rollbackAvailable should be false")
	}
	if _, ok := resp.Config["REPLAY_ENABLED"]; !ok {
		t.Error("snapshot should include REPLAY_ENABLED")
	}
}

func TestAdminConfig_RuntimePostAppliesAndRollback(t *testing.T) {
	svc, state := newTestService(t, adminCfg())
	if state.ReplayEnabled {
		t.Fatal("setup: replay should start disabled")
	}

	// 改 REPLAY_ENABLED=true。
	w := doAuth(svc, http.MethodPost, "/admin/runtime-config", "secret", `{"REPLAY_ENABLED": true}`)
	if w.Code != 200 {
		t.Fatalf("post status = %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		OK                bool     `json:"ok"`
		UpdatedKeys       []string `json:"updatedKeys"`
		RollbackAvailable bool     `json:"rollbackAvailable"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.OK || !contains(resp.UpdatedKeys, "REPLAY_ENABLED") || !resp.RollbackAvailable {
		t.Fatalf("unexpected post response: %s", w.Body.String())
	}
	if !state.ReplayEnabled {
		t.Error("live state should reflect REPLAY_ENABLED=true")
	}

	// 回滚 → 恢复 false。
	w = doAuth(svc, http.MethodPost, "/admin/runtime-config/rollback", "secret", "")
	if w.Code != 200 {
		t.Fatalf("rollback status = %d body=%s", w.Code, w.Body.String())
	}
	if state.ReplayEnabled {
		t.Error("rollback should restore REPLAY_ENABLED=false")
	}
}

func TestAdminConfig_RuntimePostInvalid(t *testing.T) {
	svc, _ := newTestService(t, adminCfg())
	// PORT 是 startup-only；应 400 且归类到 startupOnlyKeys。
	w := doAuth(svc, http.MethodPost, "/admin/runtime-config", "secret", `{"PORT": 12346}`)
	if w.Code != 400 {
		t.Fatalf("startup-only key should be 400, got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		OK              bool     `json:"ok"`
		Error           string   `json:"error"`
		StartupOnlyKeys []string `json:"startupOnlyKeys"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.OK || resp.Error != "bad-runtime-config" || !contains(resp.StartupOnlyKeys, "PORT") {
		t.Fatalf("unexpected error response: %s", w.Body.String())
	}
}

func TestAdminConfig_RollbackUnavailable(t *testing.T) {
	svc, _ := newTestService(t, adminCfg())
	w := doAuth(svc, http.MethodPost, "/admin/runtime-config/rollback", "secret", "")
	if w.Code != http.StatusConflict {
		t.Errorf("rollback with no prior change should be 409, got %d", w.Code)
	}
}

func TestAdminConfig_ReplayToggle(t *testing.T) {
	svc, state := newTestService(t, adminCfg())

	w := doAuth(svc, http.MethodGet, "/admin/replay/config", "secret", "")
	var g struct {
		Enabled bool `json:"enabled"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &g)
	if g.Enabled {
		t.Fatal("replay should start disabled")
	}

	if w := doAuth(svc, http.MethodPost, "/admin/replay/config", "secret", `{"enabled": true}`); w.Code != 200 {
		t.Fatalf("toggle status = %d body=%s", w.Code, w.Body.String())
	}
	if !state.ReplayEnabled {
		t.Error("replay should be enabled after toggle")
	}

	// 缺 enabled → 400。
	if w := doAuth(svc, http.MethodPost, "/admin/replay/config", "secret", `{}`); w.Code != 400 {
		t.Errorf("missing enabled should be 400, got %d", w.Code)
	}
}

func TestAdminConfig_RoomCreationToggle(t *testing.T) {
	svc, state := newTestService(t, adminCfg())
	if !state.RoomCreationEnabled {
		t.Fatal("room creation should start enabled (default)")
	}
	if w := doAuth(svc, http.MethodPost, "/admin/room-creation/config", "secret", `{"enabled": false}`); w.Code != 200 {
		t.Fatalf("toggle status = %d body=%s", w.Code, w.Body.String())
	}
	if state.RoomCreationEnabled {
		t.Error("room creation should be disabled after toggle")
	}
}

func contains(s []string, v string) bool {
	return slices.Contains(s, v)
}
