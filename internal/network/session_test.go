package network

import (
	"net"
	"testing"
)

// stringAddr 是用于测试的 net.Addr 实现，返回固定字符串。
type stringAddr string

func (s stringAddr) Network() string { return "test" }
func (s stringAddr) String() string  { return string(s) }

// TestSplitHostPort_Variants 验证 splitHostPort 对各种 addr 格式的处理。
func TestSplitHostPort_Variants(t *testing.T) {
	cases := []struct {
		name     string
		addr     net.Addr
		wantHost string
		wantPort int
	}{
		{"nil addr", nil, "", 0},
		{"ipv4:port", &net.TCPAddr{IP: net.IPv4(1, 2, 3, 4), Port: 5678}, "1.2.3.4", 5678},
		{"ipv6:port", &net.TCPAddr{IP: net.ParseIP("::1"), Port: 8080}, "::1", 8080},
		{"no port string", stringAddr("noport"), "noport", 0},
		{"malformed no colon", stringAddr("just-host"), "just-host", 0},
		{"non-numeric port", stringAddr("host:abc"), "host", 0},
		{"empty string addr", stringAddr(""), "", 0},
		{"trailing colon", stringAddr("host:"), "host", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host, port := splitHostPort(tc.addr)
			if host != tc.wantHost {
				t.Errorf("host = %q, want %q", host, tc.wantHost)
			}
			if port != tc.wantPort {
				t.Errorf("port = %d, want %d", port, tc.wantPort)
			}
		})
	}
}

// TestSplitHostPort_NilAddrDoesNotPanic 验证 nil addr 不会 panic。
func TestSplitHostPort_NilAddrDoesNotPanic(t *testing.T) {
	defer func() {
		if rec := recover(); rec != nil {
			t.Errorf("splitHostPort(nil) should not panic, got %v", rec)
		}
	}()
	host, port := splitHostPort(nil)
	if host != "" || port != 0 {
		t.Errorf("nil addr: got host=%q port=%d, want empty/0", host, port)
	}
}

// TestNewSession_AssignsID 验证 newSession 生成唯一 ID 并初始化字段。
func TestNewSession_AssignsID(t *testing.T) {
	// 用 net.Pipe 构造一对连接，取一端做 session
	serverConn, _ := net.Pipe()
	defer serverConn.Close()

	// newSession 需要 *server.ServerState 和 *server.Hub，这里只验证 ID 与 sendCh/done 初始化。
	// 由于 server 包构造较重，这里仅做轻量验证：通过 reflect 或直接构造。
	// 改为直接验证 splitHostPort + protocol.NewUUID 的组合行为。
	s1 := newSession(serverConn, nil, nil)
	s2 := newSession(serverConn, nil, nil)
	if s1.ID() == "" {
		t.Error("session ID should not be empty")
	}
	if s1.ID() == s2.ID() {
		t.Error("two sessions should have different IDs")
	}
	if s1.sendCh == nil {
		t.Error("sendCh should be initialized")
	}
	if s1.done == nil {
		t.Error("done channel should be initialized")
	}
	if cap(s1.sendCh) != sendChanBuffer {
		t.Errorf("sendCh buffer = %d, want %d", cap(s1.sendCh), sendChanBuffer)
	}
}
