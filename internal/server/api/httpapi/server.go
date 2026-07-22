// Package httpapi 提供 HTTP 查询/管理服务（标准库 net/http）。
//
// 含服务骨架（CORS + 每 IP 限流 + 优雅关闭）、公开路由（房间列表 / 开关配置 / 回放 / 统计）、
// 管理路由（/admin/*，需鉴权）与 WebSocket 实时推送。
package httpapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/agent/ipc"
	"github.com/Pimeng/gooophira-mp/internal/common/platform/l10n"
	"github.com/Pimeng/gooophira-mp/internal/common/platform/procstats"
	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/core/server"
)

// Service 是 HTTP 服务实例。
type Service struct {
	state         *server.ServerState
	hub           *server.Hub
	rl            *rateLimiter
	auth          *adminAuth
	ws            *wsHub
	statsProc     *procstats.Sampler
	statsProvider StatsProvider
	agent         *agentipc.Service
	pprofURL      string
	http          *http.Server

	roomCacheMu sync.Mutex
	roomCache   []byte
	roomCacheAt time.Time

	replayMu       sync.Mutex
	replaySessions map[string]replaySession // 回放下载会话（token -> 用户+过期）

	otpMu       sync.Mutex
	otpSessions map[string]otpSession // ssid -> {otp, 过期}
	otpFailIP   map[string]int        // 每 IP 失败次数
	otpFailSSID map[string]int        // 每会话失败次数
	otpBanIP    map[string]int64      // IP 封禁至（ms）
	otpBanSSID  map[string]int64      // 会话封禁至（ms）

	// cleanup 独立 goroutine 定期回收过期 replay/otp 会话与封禁项，
	// 避免长期无新请求时过期项不回收导致 map 膨胀（原先只在触发式 prune 时清理）。
	cleanupStop chan struct{}
	cleanupDone chan struct{}

	startedAt time.Time // 启动时间（用于 /admin/metrics uptime）
}

// SetAgentService 通过现有的鉴权管理指标公开可选 Agent 的连接状态。
// service 为 nil 表示 Agent IPC 已禁用或启动失败。
func (s *Service) SetAgentService(agent *agentipc.Service) { s.agent = agent }

// cleanupInterval 是 replay/otp 过期项独立清理的间隔（短于 replaySessionTTL=30min 与 OTP 封禁时长）。
const cleanupInterval = 5 * time.Minute

// New 创建 HTTP 服务（未启动）。statsProvider 为 nil 时统计端点返回 503。
// 同时把 WebSocket hub 注入 state.WSService。
func New(state *server.ServerState, hub *server.Hub, statsProvider StatsProvider, pprofURL ...string) *Service {
	cfg := state.Config
	s := &Service{
		state: state,
		hub:   hub,
		rl: newRateLimiter(
			cfg.EffectiveHTTPRateLimitMaxRequests(),
			time.Duration(cfg.EffectiveHTTPRateLimitWindowMS())*time.Millisecond,
		),
		auth:           newAdminAuth(),
		statsProvider:  statsProvider,
		replaySessions: make(map[string]replaySession),
		otpSessions:    make(map[string]otpSession),
		otpFailIP:      make(map[string]int),
		otpFailSSID:    make(map[string]int),
		otpBanIP:       make(map[string]int64),
		otpBanSSID:     make(map[string]int64),
		startedAt:      time.Now(),
	}
	if len(pprofURL) > 0 {
		s.pprofURL = pprofURL[0]
	}
	s.ws = newWSHub(s)
	s.statsProc = procstats.Start()
	state.WSService = s.ws
	// 配置热重载时更新 HTTP 限流阈值/窗口。
	state.OnConfigReload(func(c *config.ServerConfig) {
		s.rl.setOptions(c.EffectiveHTTPRateLimitMaxRequests(), time.Duration(c.EffectiveHTTPRateLimitWindowMS())*time.Millisecond)
	})
	s.startCleanup()
	return s
}

// startCleanup 启动定期清理 goroutine，回收过期 replay/otp 会话与封禁项。
// 原实现仅在 createReplaySession/handleOTPRequest 触发时顺手 prune，
// 长期无新请求时过期项不回收；此 goroutine 保证最长 cleanupInterval 后必清理一次。
func (s *Service) startCleanup() {
	s.cleanupStop = make(chan struct{})
	s.cleanupDone = make(chan struct{})
	go func() {
		defer close(s.cleanupDone)
		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.cleanupStop:
				return
			case <-ticker.C:
				s.replayMu.Lock()
				s.pruneReplaySessionsLocked()
				s.replayMu.Unlock()
				s.otpMu.Lock()
				s.pruneOTPLocked()
				s.otpMu.Unlock()
			}
		}
	}()
}

// Start 在 addr 上监听并开始服务（非阻塞）。
func (s *Service) Start(addr string) (net.Addr, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	s.http = &http.Server{Handler: s.handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.http.Serve(ln) }()
	return ln.Addr(), nil
}

// Close 优雅关闭 HTTP 与 WebSocket 服务，并停止后台采样与清理 goroutine。
func (s *Service) Close() error {
	if s.cleanupStop != nil {
		close(s.cleanupStop)
		<-s.cleanupDone
		s.cleanupStop, s.cleanupDone = nil, nil
	}
	s.ws.closeAll()
	if s.statsProc != nil {
		s.statsProc.Stop()
	}
	if s.http == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.http.Shutdown(ctx)
}

func (s *Service) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api")
		if path == "" {
			path = "/"
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = path
		s.route(w, r2)
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.URL.Path = strings.TrimPrefix(r.URL.Path, "/api")
		s.route(w, r2)
	})
	return mux
}

// route 是统一入口：CORS → 限流 → 分发。
func (s *Service) route(w http.ResponseWriter, r *http.Request) {
	lang := s.langFor(r)
	s.applyCORS(w, r)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	ip := clientIP(r, s.state.Config.EffectiveRealIPHeader())

	// WebSocket 升级走独立路径（连接长存，不计入普通限流）。
	if r.URL.Path == "/ws" {
		s.ws.handle(w, r, ip)
		return
	}

	if !s.rl.allow(ip) {
		w.Header().Set("retry-after", "60")
		s.writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"ok": false, "error": "rate-limited", "message": l10n.TL(lang, "http-rate-limited", nil),
		})
		return
	}

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/room":
		s.handleRoomList(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/room-creation/config":
		s.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": s.state.RoomCreationEnabled})
	case r.Method == http.MethodGet && r.URL.Path == "/replay/config":
		s.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": s.state.ReplayEnabled})
	case strings.HasPrefix(r.URL.Path, "/replay/"):
		if !s.routeReplay(w, r, lang) {
			s.writeJSON(w, http.StatusNotFound, map[string]any{
				"ok": false, "error": "not-found", "message": l10n.TL(lang, "http-not-found", nil),
			})
		}
	case r.Method == http.MethodPost && (r.URL.Path == "/admin/otp/request" || r.URL.Path == "/admin/otp/verify"):
		// OTP 提权路由必须在 admin 鉴权之前——它正是用来获取 admin 访问的途径。
		s.routeOTP(w, r, lang, ip)
	case strings.HasPrefix(r.URL.Path, "/admin/"):
		if !s.checkAdmin(w, r, ip, lang) {
			return
		}
		if !s.routeAdmin(w, r, lang) {
			s.writeJSON(w, http.StatusNotFound, map[string]any{
				"ok": false, "error": "not-found", "message": l10n.TL(lang, "http-not-found", nil),
			})
		}
	case r.Method == http.MethodGet && r.URL.Path == "/charts/hot":
		s.handleChartsHot(w, r)
	case strings.HasPrefix(r.URL.Path, "/chart/"):
		s.handleChart(w, r)
	case strings.HasPrefix(r.URL.Path, "/player/"):
		s.handlePlayer(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/leaderboard":
		s.handleLeaderboard(w, r)
	default:
		w.Header().Set("content-type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(l10n.TL(lang, "http-not-found", nil)))
	}
}

func (s *Service) langFor(r *http.Request) *l10n.Language {
	if al := r.Header.Get("Accept-Language"); al != "" {
		// 取第一段（忽略 q 权重）作为提示。
		first := strings.TrimSpace(strings.Split(al, ",")[0])
		return l10n.NewLanguage(first)
	}
	return s.state.ServerLang
}

func (s *Service) applyCORS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return
	}
	for _, o := range s.state.Config.EffectiveCorsOrigins() {
		if o == "*" {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Admin-Token")
			return
		}
		if o == origin {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Admin-Token")
			return
		}
	}
}

func (s *Service) writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.Header().Set("x-content-type-options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (s *Service) encode(body any) []byte {
	buf, err := json.Marshal(body)
	if err != nil {
		return []byte("{}")
	}
	return buf
}

// writeRaw 写入预先编码好的 JSON 字节（用于缓存命中场景）。
func (s *Service) writeRaw(w http.ResponseWriter, buf []byte) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.Header().Set("cache-control", "no-store")
	w.Header().Set("x-content-type-options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf)
}

func clientIP(r *http.Request, realIPHeader string) string {
	if realIPHeader != "" {
		if v := r.Header.Get(realIPHeader); v != "" {
			return strings.TrimSpace(strings.Split(v, ",")[0]) // X-Forwarded-For 取首个
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---------- 每 IP 固定窗口限流 ----------

type rateLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string]*ipWindow
}

type ipWindow struct {
	count int
	reset time.Time
}

func newRateLimiter(maxReq int, window time.Duration) *rateLimiter {
	return &rateLimiter{max: maxReq, window: window, hits: make(map[string]*ipWindow)}
}

// setOptions 运行时更新限流阈值与窗口（配置热重载用）。不重置已有计数窗口。
func (r *rateLimiter) setOptions(maxReq int, window time.Duration) {
	r.mu.Lock()
	r.max, r.window = maxReq, window
	r.mu.Unlock()
}

func (r *rateLimiter) allow(ip string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	w := r.hits[ip]
	if w == nil || now.After(w.reset) {
		r.hits[ip] = &ipWindow{count: 1, reset: now.Add(r.window)}
		r.gc(now)
		return true
	}
	if w.count >= r.max {
		return false
	}
	w.count++
	return true
}

// gc 顺带清理已过期的窗口，防止 map 无限增长（调用方持锁）。
func (r *rateLimiter) gc(now time.Time) {
	if len(r.hits) < 1024 {
		return
	}
	for ip, w := range r.hits {
		if now.After(w.reset) {
			delete(r.hits, ip)
		}
	}
}
