package httpapi

import (
	"embed"
	"net/http"
)

//go:embed guipage.html guipage.css guipage.js
var guiAssets embed.FS

// handleGUIPage 提供服务端 GUI 控制台页面（GET /gui、/gui/），对应 TS routes/guiRoutes.ts。
// 页面本身公开（不含敏感数据）；其内部数据接口（/admin/* 与 /ws 订阅）均需管理员 token。
func (s *Service) handleGUIPage(w http.ResponseWriter) {
	s.serveGUIAsset(w, "guipage.html", "text/html; charset=utf-8")
}

func (s *Service) handleGUIAsset(w http.ResponseWriter, path string) bool {
	switch path {
	case "/gui/guipage.css":
		s.serveGUIAsset(w, "guipage.css", "text/css; charset=utf-8")
	case "/gui/guipage.js":
		s.serveGUIAsset(w, "guipage.js", "text/javascript; charset=utf-8")
	default:
		return false
	}
	return true
}

func (s *Service) serveGUIAsset(w http.ResponseWriter, name, contentType string) {
	h := w.Header()
	h.Set("content-type", contentType)
	h.Set("cache-control", "no-store")
	h.Set("x-content-type-options", "nosniff")
	body, err := guiAssets.ReadFile(name)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
