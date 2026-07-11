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
		for _, marker := range []string{"id=\"app\"", "href=\"/gui/guipage.css\"", "src=\"/gui/guipage.js\""} {
			if !strings.Contains(body, marker) {
				t.Errorf("%s body missing marker %q", p, marker)
			}
		}
		if strings.Contains(body, "<style>") || strings.Contains(body, "<script>") {
			t.Errorf("%s body should not contain inline CSS or JavaScript", p)
		}
	}
}

func TestGUIPage_EmbeddedAssets(t *testing.T) {
	svc, _ := newTestService(t, adminCfg())
	tests := []struct {
		path        string
		contentType string
		markers     []string
	}{
		{"/gui/guipage.css", "text/css", []string{"--chart-mem", "#login", ".console"}},
		{"/gui/guipage.js", "text/javascript", []string{"/admin/metrics", "console_subscribe", "phira_mp_admin_token"}},
	}
	for _, tt := range tests {
		w := do(svc, http.MethodGet, tt.path)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d", tt.path, w.Code)
		}
		if ct := w.Header().Get("content-type"); !strings.HasPrefix(ct, tt.contentType) {
			t.Errorf("%s content-type = %q, want %s", tt.path, ct, tt.contentType)
		}
		if w.Header().Get("cache-control") != "no-store" {
			t.Errorf("%s cache-control = %q, want no-store", tt.path, w.Header().Get("cache-control"))
		}
		if w.Header().Get("x-content-type-options") != "nosniff" {
			t.Errorf("%s missing nosniff header", tt.path)
		}
		for _, marker := range tt.markers {
			if !strings.Contains(w.Body.String(), marker) {
				t.Errorf("%s body missing marker %q", tt.path, marker)
			}
		}
	}
}
