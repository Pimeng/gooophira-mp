package network

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"testing"
	"testing/iotest"
)

// parseFrom 在 data 上跑解析器，返回结果与解析后仍可读的剩余字节。
func parseFrom(t *testing.T, data []byte) (*ProxyInfo, []byte) {
	t.Helper()
	r := bufio.NewReader(bytes.NewReader(data))
	info := ParseProxyHeader(r)
	rest, _ := io.ReadAll(r)
	return info, rest
}

// buildV2 拼装一个 v2 二进制头（签名 + verCmd + famProto + addrLen + 地址块）。
func buildV2(verCmd, famProto byte, addr []byte) []byte {
	h := make([]byte, 16)
	copy(h, proxyV2Signature)
	h[12] = verCmd
	h[13] = famProto
	binary.BigEndian.PutUint16(h[14:16], uint16(len(addr)))
	return append(h, addr...)
}

func TestProxy_V1TCP4(t *testing.T) {
	info, rest := parseFrom(t, []byte("PROXY TCP4 192.168.0.1 192.168.0.11 56324 443\r\nHELLO"))
	if info == nil {
		t.Fatal("expected ProxyInfo, got nil")
	}
	if info.SourceAddr != "192.168.0.1" || info.SourcePort != 56324 {
		t.Errorf("source = %s:%d, want 192.168.0.1:56324", info.SourceAddr, info.SourcePort)
	}
	if info.DestAddr != "192.168.0.11" || info.DestPort != 443 {
		t.Errorf("dest = %s:%d, want 192.168.0.11:443", info.DestAddr, info.DestPort)
	}
	if info.Family != "TCP4" {
		t.Errorf("family = %s, want TCP4", info.Family)
	}
	if string(rest) != "HELLO" { // 头之后的字节必须原样保留给握手
		t.Errorf("remaining = %q, want %q", rest, "HELLO")
	}
}

func TestProxy_V1TCP6(t *testing.T) {
	info, _ := parseFrom(t, []byte("PROXY TCP6 ::1 ::1 56324 443\r\n"))
	if info == nil {
		t.Fatal("expected ProxyInfo, got nil")
	}
	if info.SourceAddr != "::1" {
		t.Errorf("source = %s, want ::1", info.SourceAddr)
	}
	if info.Family != "TCP6" {
		t.Errorf("family = %s, want TCP6", info.Family)
	}
}

func TestProxy_V1Unknown(t *testing.T) {
	info, _ := parseFrom(t, []byte("PROXY UNKNOWN\r\n"))
	if info != nil {
		t.Errorf("UNKNOWN should yield nil, got %+v", info)
	}
}

func TestProxy_V1Malformed(t *testing.T) {
	// 字段数不足（应为 6）→ nil；但这是确认过的 v1 头，整行应被消费。
	info, rest := parseFrom(t, []byte("PROXY TCP4 1.2.3.4\r\nNEXT"))
	if info != nil {
		t.Errorf("malformed v1 should yield nil, got %+v", info)
	}
	if string(rest) != "NEXT" {
		t.Errorf("remaining = %q, want %q", rest, "NEXT")
	}
}

func TestProxy_NonProxyNotConsumed(t *testing.T) {
	// 非 PROXY 数据：返回 nil 且一个字节都不能消费，握手逻辑要能读到原始字节。
	in := []byte("GET / HTTP/1.1\r\n")
	info, rest := parseFrom(t, in)
	if info != nil {
		t.Errorf("non-proxy should yield nil, got %+v", info)
	}
	if !bytes.Equal(rest, in) {
		t.Errorf("non-proxy data must not be consumed: remaining = %q", rest)
	}
}

func TestProxy_LeadingPButNotProxy(t *testing.T) {
	// 以 'P' 开头但不是 "PROXY "：不消费。
	in := []byte("PING\r\n")
	info, rest := parseFrom(t, in)
	if info != nil {
		t.Errorf("non-PROXY 'P...' should yield nil, got %+v", info)
	}
	if !bytes.Equal(rest, in) {
		t.Errorf("must not be consumed: remaining = %q", rest)
	}
}

func TestProxy_V2TCP4(t *testing.T) {
	addr := make([]byte, 12)
	addr[0], addr[1], addr[2], addr[3] = 192, 168, 0, 1
	addr[4], addr[5], addr[6], addr[7] = 192, 168, 0, 11
	binary.BigEndian.PutUint16(addr[8:10], 56324)
	binary.BigEndian.PutUint16(addr[10:12], 443)
	header := buildV2(0x21, 0x11, addr) // ver=2 cmd=PROXY, TCP over IPv4

	info, rest := parseFrom(t, append(header, []byte("TAIL")...))
	if info == nil {
		t.Fatal("expected ProxyInfo, got nil")
	}
	if info.SourceAddr != "192.168.0.1" || info.SourcePort != 56324 {
		t.Errorf("source = %s:%d, want 192.168.0.1:56324", info.SourceAddr, info.SourcePort)
	}
	if info.DestAddr != "192.168.0.11" || info.DestPort != 443 {
		t.Errorf("dest = %s:%d, want 192.168.0.11:443", info.DestAddr, info.DestPort)
	}
	if info.Family != "TCP4" {
		t.Errorf("family = %s, want TCP4", info.Family)
	}
	if string(rest) != "TAIL" {
		t.Errorf("remaining = %q, want TAIL", rest)
	}
}

func TestProxy_V2TCP6(t *testing.T) {
	addr := make([]byte, 36)
	// ::1 作为源地址（最后一个字节为 1）。
	addr[15] = 1
	addr[31] = 1 // dest ::1
	binary.BigEndian.PutUint16(addr[32:34], 1234)
	binary.BigEndian.PutUint16(addr[34:36], 5678)
	header := buildV2(0x21, 0x21, addr) // ver=2 cmd=PROXY, TCP over IPv6

	info, _ := parseFrom(t, header)
	if info == nil {
		t.Fatal("expected ProxyInfo, got nil")
	}
	if info.SourceAddr != "::1" { // 规范形式（优于 TS 的 0:0:0:0:0:0:0:1）
		t.Errorf("source = %s, want ::1", info.SourceAddr)
	}
	if info.SourcePort != 1234 || info.Family != "TCP6" {
		t.Errorf("port/family = %d/%s, want 1234/TCP6", info.SourcePort, info.Family)
	}
}

func TestProxy_V2Local(t *testing.T) {
	// LOCAL 命令（cmd=0x00）：无地址 → nil，但整个头应被消费。
	header := buildV2(0x20, 0x00, nil)
	info, rest := parseFrom(t, append(header, []byte("X")...))
	if info != nil {
		t.Errorf("LOCAL should yield nil, got %+v", info)
	}
	if string(rest) != "X" {
		t.Errorf("remaining = %q, want X (header consumed)", rest)
	}
}

func TestProxy_V2WrongVersion(t *testing.T) {
	// 版本号非 2（高 4 位=1）：不识别为 v2，不消费。
	header := buildV2(0x11, 0x11, make([]byte, 12))
	info, rest := parseFrom(t, header)
	if info != nil {
		t.Errorf("wrong version should yield nil, got %+v", info)
	}
	if len(rest) != len(header) {
		t.Errorf("wrong-version header must not be consumed, consumed %d bytes", len(header)-len(rest))
	}
}

func TestProxy_EmptyInput(t *testing.T) {
	info, _ := parseFrom(t, nil)
	if info != nil {
		t.Errorf("empty input should yield nil, got %+v", info)
	}
}

func TestProxy_V1Fragmented(t *testing.T) {
	// 逐字节到达也应能解析（Peek 内部会循环填充）。
	data := []byte("PROXY TCP4 10.0.0.1 10.0.0.2 1234 5678\r\n")
	r := bufio.NewReader(iotest.OneByteReader(bytes.NewReader(data)))
	info := ParseProxyHeader(r)
	if info == nil {
		t.Fatal("fragmented v1 should still parse")
	}
	if info.SourceAddr != "10.0.0.1" || info.SourcePort != 1234 {
		t.Errorf("source = %s:%d, want 10.0.0.1:1234", info.SourceAddr, info.SourcePort)
	}
}

func TestProxy_V2Fragmented(t *testing.T) {
	addr := make([]byte, 12)
	addr[0], addr[1], addr[2], addr[3] = 10, 0, 0, 7
	binary.BigEndian.PutUint16(addr[8:10], 4000)
	header := buildV2(0x21, 0x11, addr)
	r := bufio.NewReader(iotest.OneByteReader(bytes.NewReader(header)))
	info := ParseProxyHeader(r)
	if info == nil {
		t.Fatal("fragmented v2 should still parse")
	}
	if info.SourceAddr != "10.0.0.7" || info.SourcePort != 4000 {
		t.Errorf("source = %s:%d, want 10.0.0.7:4000", info.SourceAddr, info.SourcePort)
	}
}
