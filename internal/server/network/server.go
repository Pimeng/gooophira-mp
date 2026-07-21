package network

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/core/server"
)

var errEncode = errors.New("encode-server-command-failed")

const (
	// 连接速率限制参数（对应 TS server.ts ConnectionRateLimiter 配置）。
	connRateWindow  = 10 * time.Second
	connRateBan     = 30 * time.Second
	connCleanupTick = 30 * time.Second
)

// Server 是 TCP 监听器：接受连接并为每个连接启动一个 Session。
// 接受路径上做两道保护：全服并发连接硬上限（MAX_CONNECTIONS）与每 IP 连接速率限制
// （CONNECTION_RATE_LIMIT），对应 TS server.ts 的 maxConnections 与 connectionLimiter。
type Server struct {
	listener    net.Listener
	state       *server.ServerState
	hub         *server.Hub
	connLimiter *connectionRateLimiter
	activeConns int32
	done        chan struct{}
	closeOnce   sync.Once
	// sessions 跟踪所有活跃会话，供 Close 时主动关闭（触发 cleanup 走立即移除路径，
	// 避免 dangleTimer 在进程退出后触发访问不稳定状态）。
	sessions sync.Map
}

// Listen 在 addr 上监听 TCP 并开始接受连接。
func Listen(addr string, state *server.ServerState, hub *server.Hub) (*Server, error) {
	return ListenConfig(addr, nil, state, hub)
}

// ListenConfig 使用可选的 net.ListenConfig（bigBacklog 为 true 时设大 SO_RCVBUF）监听 TCP。
func ListenConfig(addr string, lc *net.ListenConfig, state *server.ServerState, hub *server.Hub) (*Server, error) {
	if lc == nil {
		lc = &net.ListenConfig{}
	}
	ln, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return nil, err
	}
	s := &Server{
		listener:    ln,
		state:       state,
		hub:         hub,
		connLimiter: newConnectionRateLimiter(state.Config.EffectiveConnectionRateLimit(), connRateWindow, connRateBan),
		done:        make(chan struct{}),
	}
	// 配置热重载时更新连接限速阈值（限速器实例常驻，仅改阈值）。
	state.OnConfigReload(func(c *config.ServerConfig) { s.connLimiter.setMaxConns(c.EffectiveConnectionRateLimit()) })
	go s.acceptLoop()
	go s.acceptLoop()
	go s.cleanupLoop()
	return s, nil
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return // 监听器已关闭
		}
		if !s.admit(conn) {
			continue
		}
		sess := newSession(conn, s.state, s.hub)
		s.sessions.Store(sess, struct{}{})
		go func() {
			defer atomic.AddInt32(&s.activeConns, -1)
			defer s.sessions.Delete(sess)
			sess.run()
		}()
	}
}

// admit 决定是否接纳新连接：先占用并发名额做硬上限判定，再按 TCP 对端 IP 限速。
// 任一不通过则归还名额、关闭连接并返回 false。通过则保留名额（由会话 goroutine 退出时归还）。
func (s *Server) admit(conn net.Conn) bool {
	n := atomic.AddInt32(&s.activeConns, 1)
	if max := s.state.Config.EffectiveMaxConnections(); max >= 1 && int(n) > max {
		atomic.AddInt32(&s.activeConns, -1)
		s.debug("connection rejected: max connections (" + strconv.Itoa(max) + ") reached")
		_ = conn.Close()
		return false
	}
	ip := hostOf(conn.RemoteAddr())
	// 回环连接（本机压测 / 同机反代 / 本地 GUI）豁免连接限速：回环不可能是远程连接洪水，
	// 对其限速无任何安全收益，反而会在本机高频建连时把自己封掉，连坐同样走回环的真实客户端。
	if !isLoopbackHost(ip) && !s.connLimiter.allow(ip, time.Now()) {
		atomic.AddInt32(&s.activeConns, -1)
		s.debug("rate-limited connection from " + ip)
		_ = conn.Close()
		return false
	}
	return true
}

// cleanupLoop 定期清理连接限速器中已过期的窗口/封禁项，直至 Server 关闭。
func (s *Server) cleanupLoop() {
	ticker := time.NewTicker(connCleanupTick)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			s.connLimiter.cleanup(time.Now())
		}
	}
}

func (s *Server) debug(msg string) {
	if lg := s.state.Logger; lg != nil {
		lg.Debug(msg)
	}
}

// Addr 返回实际监听地址（端口为 0 时可用于获取系统分配的端口）。
func (s *Server) Addr() net.Addr { return s.listener.Addr() }

// Close 停止接受新连接、结束后台清理，并主动关闭所有活跃会话。
// 主动关闭会话触发 cleanup 走立即移除路径（closing 标志），避免 dangleTimer
// 在进程退出后触发访问不稳定状态。
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		close(s.done)
		s.listener.Close()
		s.closeAllSessions()
	})
	return nil
}

// closeAllSessions 遍历所有活跃会话调用 closeForShutdown。
func (s *Server) closeAllSessions() {
	s.sessions.Range(func(key, _ any) bool {
		if sess, ok := key.(*Session); ok {
			sess.closeForShutdown()
		}
		return true
	})
}

// hostOf 取地址的主机部分（去掉端口）；解析失败时回退原始字符串。
func hostOf(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	return host
}

// isLoopbackHost 判断主机字符串是否为回环地址（127.0.0.0/8、::1）。非合法 IP 返回 false。
func isLoopbackHost(ip string) bool {
	p := net.ParseIP(ip)
	return p != nil && p.IsLoopback()
}
