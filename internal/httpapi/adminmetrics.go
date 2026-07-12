package httpapi

import (
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/l10n"
)

// handleAdminMetrics 返回服务器性能与业务监控指标（对应 TS adminMetricsRoutes.ts）。
//
// memory.rss / cpu.percent / history 由 procstats 采样器提供，字段命名与 GUI 图表契约一致；
// 同时保留 Go 原生运行时指标（alloc/goroutines/numGC 等）作为补充。?history=1 时附带历史采样。
func (s *Service) handleAdminMetrics(w http.ResponseWriter, r *http.Request, _ *l10n.Language) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	rss, heapUsed, heapTotal := s.statsProc.LiveMemory()
	cpuPercent := 0.0
	if cur, ok := s.statsProc.Current(); ok {
		cpuPercent = cur.CPUPercent
	}

	s.state.Mu.Lock()
	activeSessions := len(s.state.Sessions)
	onlineUsers := len(s.state.Users)
	activeRooms := len(s.state.Rooms)
	serverBanned := len(s.state.BannedUsers)
	roomBanned := 0
	for _, set := range s.state.BannedRoomUsers {
		roomBanned += len(set)
	}
	tempTokens := len(s.state.TempAdminTokens)
	replayEnabled := s.state.ReplayEnabled
	roomCreationEnabled := s.state.RoomCreationEnabled
	serverName := s.state.ServerName
	version := s.state.Version
	s.state.Mu.Unlock()

	resp := map[string]any{
		"ok":        true,
		"timestamp": time.Now().UnixMilli(),
		"server": map[string]any{
			"name":    serverName,
			"version": version,
		},
		"process": map[string]any{
			"pid":        os.Getpid(),
			"uptime":     time.Since(s.startedAt).Seconds(),
			"goVersion":  runtime.Version(),
			"runtime":    "Go " + strings.TrimPrefix(runtime.Version(), "go"), // GUI 副标题运行时展示
			"platform":   runtime.GOOS,
			"arch":       runtime.GOARCH,
			"goroutines": runtime.NumGoroutine(),
			"numCPU":     runtime.NumCPU(),
			"pprofURL":   s.pprofURL,
		},
		"memory": map[string]any{
			// GUI 图表契约字段：
			"rss":         rss,
			"heapUsed":    heapUsed,
			"heapTotal":   heapTotal,
			"systemTotal": s.statsProc.SystemTotalMem(),
			// Go 原生运行时补充字段：
			"alloc":      mem.Alloc,
			"totalAlloc": mem.TotalAlloc,
			"sys":        mem.Sys,
			"heapAlloc":  mem.HeapAlloc,
			"heapSys":    mem.HeapSys,
			"numGC":      mem.NumGC,
		},
		"cpu": map[string]any{
			"cores":   s.statsProc.CPUCount(),
			"percent": cpuPercent,
		},
		"business": map[string]any{
			"activeSessions":      activeSessions,
			"onlineUsers":         onlineUsers,
			"activeRooms":         activeRooms,
			"wsConnections":       s.ws.clientCount(),
			"serverBannedUsers":   serverBanned,
			"roomBannedUsers":     roomBanned,
			"tempAdminTokens":     tempTokens,
			"replayEnabled":       replayEnabled,
			"roomCreationEnabled": roomCreationEnabled,
		},
	}
	// ?history=1 附带 CPU/内存历史采样（GUI 图表回填）。
	if r.URL.Query().Get("history") == "1" {
		resp["history"] = s.statsProc.History()
	}
	s.writeJSON(w, http.StatusOK, resp)
}
