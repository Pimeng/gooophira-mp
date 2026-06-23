package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestGUIPage_ServedPublicly(t *testing.T) {
	svc, _ := newTestService(t, adminCfg()) // 即使配置了 admin token，/gui 仍公开
	for _, p := range []string{"/gui", "/gui/"} {
		w := do(svc, http.MethodGet, p)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d", p, w.Code)
		}
		if ct := w.Header().Get("content-type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s content-type = %q, want text/html", p, ct)
		}
		if w.Header().Get("x-content-type-options") != "nosniff" {
			t.Errorf("%s missing nosniff header", p)
		}
		body := w.Body.String()
		if !strings.HasPrefix(strings.TrimSpace(body), "<!doctype html>") {
			t.Errorf("%s body should start with doctype", p)
		}
		// 关键挂载点应存在（确保是完整页面而非片段）。
		for _, marker := range []string{"id=\"app\"", "/admin/metrics", "console_subscribe"} {
			if !strings.Contains(body, marker) {
				t.Errorf("%s body missing marker %q", p, marker)
			}
		}
	}
}
