package httpapi

import (
	_ "embed"
	"net/http"
)

// guiHTML 是内嵌的服务端 GUI 控制台单文件页面（零外部资源依赖），由 guipage.html 编译期嵌入。
//
//go:embed guipage.html
var guiHTML string

// handleGUIPage 提供服务端 GUI 控制台页面（GET /gui、/gui/），对应 TS routes/guiRoutes.ts。
// 页面本身公开（不含敏感数据）；其内部数据接口（/admin/* 与 /ws 订阅）均需管理员 token。
func (s *Service) handleGUIPage(w http.ResponseWriter) {
	h := w.Header()
	h.Set("content-type", "text/html; charset=utf-8")
	h.Set("cache-control", "no-store")
	h.Set("x-content-type-options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(guiHTML))
}
