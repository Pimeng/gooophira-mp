package network

import (
	"net"
	"testing"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/config"
	"github.com/Pimeng/gooophira-mp/internal/core/server"
)

// fakeConn 是仅供 admit 测试的最小 net.Conn：可设置 RemoteAddr，并记录是否被 Close。
type fakeConn struct {
	remote net.Addr
	closed bool
}

func (c *fakeConn) Read([]byte) (int, error)         { return 0, nil }
func (c *fakeConn) Write([]byte) (int, error)        { return 0, nil }
func (c *fakeConn) Close() error                     { c.closed = true; return nil }
func (c *fakeConn) LocalAddr() net.Addr              { return nil }
func (c *fakeConn) RemoteAddr() net.Addr             { return c.remote }
func (c *fakeConn) SetDeadline(time.Time) error      { return nil }
func (c *fakeConn) SetReadDeadline(time.Time) error  { return nil }
func (c *fakeConn) SetWriteDeadline(time.Time) error { return nil }

func TestIsLoopbackHost(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1": true,
		"127.0.0.5": true,
		"::1":       true,
		"1.2.3.4":   false,
		"8.8.8.8":   false,
		"":          false,
		"garbage":   false,
	}
	for ip, want := range cases {
		if got := isLoopbackHost(ip); got != want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", ip, got, want)
		}
	}
}

// TestAdmit_LoopbackExemptFromRateLimit 锁定本地压测连坐的修复：回环连接豁免连接限速
// （远超 cap 仍全部放行），而非回环 IP 仍按 cap 限速并在超限时关闭连接。
func TestAdmit_LoopbackExemptFromRateLimit(t *testing.T) {
	state := server.NewServerState(&config.ServerConfig{}, nil, "test", "", "")
	s := &Server{
		state:       state,
		connLimiter: newConnectionRateLimiter(3, 10*time.Second, 30*time.Second),
	}

	// 回环：cap=3，但豁免限速 → 连打 20 次仍全部放行。
	loop := &fakeConn{remote: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5000}}
	for i := range 20 {
		if !s.admit(loop) {
			t.Fatalf("loopback connection %d should be admitted (exempt from rate limit)", i+1)
		}
	}

	// 非回环：cap=3，第 4 个起被限速拒绝。
	remote := &fakeConn{remote: &net.TCPAddr{IP: net.IPv4(8, 8, 8, 8), Port: 6000}}
	pass := 0
	for range 20 {
		if s.admit(remote) {
			pass++
		}
	}
	if pass != 3 {
		t.Fatalf("non-loopback should be capped at 3, admitted %d", pass)
	}
	if !remote.closed {
		t.Error("rejected non-loopback conn should be Close()d")
	}
}
