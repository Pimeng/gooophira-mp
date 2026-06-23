package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// reqIP 构造一个带 X-Admin-Token 头与指定来源 IP 的请求并执行。
func reqIP(svc *Service, path, token, remoteAddr string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		r.Header.Set("X-Admin-Token", token)
	}
	r.RemoteAddr = remoteAddr
	w := httptest.NewRecorder()
	svc.route(w, r)
	return w
}

func TestAdmin_XAdminTokenHeader(t *testing.T) {
	svc, _ := newTestService(t, adminCfg())
	// GUI 使用 X-Admin-Token 头——必须被识别为有效鉴权。
	if w := reqIP(svc, "/admin/rooms", "secret", "203.0.113.7:5000"); w.Code != http.StatusOK {
		t.Fatalf("X-Admin-Token header should authenticate, got %d", w.Code)
	}
}

func TestGUILocalToken_LoopbackOnly(t *testing.T) {
	svc, state := newTestService(t, adminCfg())
	tok := "gui-local-xyz"
	state.Mu.Lock()
	state.GUILocalToken = &tok
	state.Mu.Unlock()

	// 回环来源 + GUI 本机 token → 放行。
	if w := reqIP(svc, "/admin/rooms", tok, "127.0.0.1:5000"); w.Code != http.StatusOK {
		t.Fatalf("loopback gui token should pass, got %d", w.Code)
	}
	// 非回环来源 + 同一 GUI token → 拒绝（按错误 token 处理，非 200）。
	if w := reqIP(svc, "/admin/rooms", tok, "203.0.113.7:5000"); w.Code == http.StatusOK {
		t.Error("non-loopback gui token must not pass")
	}
}
