package network

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"net"
	"strconv"
	"strings"
)

// proxyV2Signature 是 PROXY protocol v2 头的 12 字节固定前缀。
var proxyV2Signature = []byte{0x0d, 0x0a, 0x0d, 0x0a, 0x00, 0x0d, 0x0a, 0x51, 0x55, 0x49, 0x54, 0x0a}

// proxyV1MaxLen 是 v1 文本头的最大长度（含结尾 CRLF），超出即视为非法。
const proxyV1MaxLen = 108

// ProxyInfo 是从 PROXY protocol 头解析出的真实客户端连接信息。
type ProxyInfo struct {
	SourceAddr string // 真实客户端 IP
	SourcePort int    // 真实客户端端口
	DestAddr   string // 目标地址
	DestPort   int    // 目标端口
	Family     string // "TCP4" 或 "TCP6"
}

// ParseProxyHeader 尝试从 r 解析 HAProxy PROXY protocol 头（v1 文本或 v2 二进制）。
//
// 成功解析出地址时返回 *ProxyInfo，并从 r 恰好消费掉整个头，使后续读取从真正的
// 客户端握手字节开始；遇到非 PROXY 数据、UNKNOWN、LOCAL 命令或不支持的地址族则返回
// nil（此时若确认为 PROXY 头会消费掉头本身，否则不消费已 Peek 的字节），调用方应回退
// 使用 TCP 对端地址继续。读取超时由调用方在底层 conn 上设置截止时间控制。
//
// 对应 TS proxyProtocol.ts parseProxyProtocol（Go 用 bufio.Reader 的 Peek/Discard 取代
// socket.unshift 的“把多读的字节塞回去”语义）。
func ParseProxyHeader(r *bufio.Reader) *ProxyInfo {
	prefix, err := r.Peek(1)
	if err != nil {
		return nil
	}
	switch prefix[0] {
	case proxyV2Signature[0]: // 0x0d → 可能是 v2 二进制头
		return parseProxyV2(r)
	case 'P': // 可能是 v1 文本头 "PROXY "
		return parseProxyV1(r)
	default:
		return nil // 既非 v1 也非 v2：不消费，交给握手逻辑
	}
}

// parseProxyV2 解析 v2 二进制头。仅在 12 字节签名与版本号校验通过后才消费字节。
func parseProxyV2(r *bufio.Reader) *ProxyInfo {
	head, err := r.Peek(16) // 12 签名 + verCmd + famProto + 2 字节 addrLen
	if err != nil || !bytes.Equal(head[:12], proxyV2Signature) {
		return nil
	}
	verCmd := head[12]
	if verCmd>>4 != 2 { // 高 4 位为版本，必须是 2
		return nil
	}
	command := verCmd & 0x0f
	famProto := head[13]
	addrLen := int(binary.BigEndian.Uint16(head[14:16]))
	total := 16 + addrLen

	full, err := r.Peek(total)
	if err != nil {
		// 头未完整到达，或 addrLen 超出读缓冲（恶意巨值）：放弃且不消费，回退 TCP 对端地址。
		return nil
	}
	// 至此确认是一个完整的 v2 头：无论能否解出地址都消费掉整个头。
	_, _ = r.Discard(total)

	if command != 0x01 { // 0x00 LOCAL（无地址）或其他命令：消费后回退
		return nil
	}
	family := famProto >> 4
	proto := famProto & 0x0f
	switch {
	case family == 0x01 && proto == 0x01: // TCP over IPv4
		if addrLen < 12 {
			return nil
		}
		return &ProxyInfo{
			SourceAddr: net.IPv4(full[16], full[17], full[18], full[19]).String(),
			DestAddr:   net.IPv4(full[20], full[21], full[22], full[23]).String(),
			SourcePort: int(binary.BigEndian.Uint16(full[24:26])),
			DestPort:   int(binary.BigEndian.Uint16(full[26:28])),
			Family:     "TCP4",
		}
	case family == 0x02 && proto == 0x01: // TCP over IPv6
		if addrLen < 36 {
			return nil
		}
		return &ProxyInfo{
			SourceAddr: ipv6String(full[16:32]),
			DestAddr:   ipv6String(full[32:48]),
			SourcePort: int(binary.BigEndian.Uint16(full[48:50])),
			DestPort:   int(binary.BigEndian.Uint16(full[50:52])),
			Family:     "TCP6",
		}
	default:
		return nil // 不支持的地址族/协议（头已消费）
	}
}

// parseProxyV1 解析 v1 文本头。仅在确认 "PROXY " 前缀且找到 CRLF 后才消费整行。
func parseProxyV1(r *bufio.Reader) *ProxyInfo {
	buf, _ := r.Peek(proxyV1MaxLen) // 可能短读（如代理未续发握手字节）：忽略 err，只要含 CRLF 即可解析
	idx := bytes.Index(buf, []byte("\r\n"))
	if idx < 0 {
		return nil // 限定长度内无 CRLF：非合法 v1 头，不消费
	}
	line := string(buf[:idx])
	if !strings.HasPrefix(line, "PROXY ") {
		return nil // 以 'P' 开头但不是 "PROXY "：不消费
	}
	_, _ = r.Discard(idx + 2) // 确认为 v1 头：消费整行（含 CRLF）
	return parseProxyV1Line(line)
}

// parseProxyV1Line 解析形如 "PROXY TCP4 src dst sport dport" 的 v1 头行。
// UNKNOWN、字段数不符、未知地址族或端口非法均返回 nil（行已被调用方消费，回退 TCP 对端地址）。
func parseProxyV1Line(line string) *ProxyInfo {
	parts := strings.Split(line, " ")
	if len(parts) < 2 || parts[1] == "UNKNOWN" {
		return nil
	}
	if len(parts) != 6 {
		return nil
	}
	family := parts[1]
	if family != "TCP4" && family != "TCP6" {
		return nil
	}
	srcPort, err1 := strconv.Atoi(parts[4])
	dstPort, err2 := strconv.Atoi(parts[5])
	if parts[2] == "" || parts[3] == "" || err1 != nil || err2 != nil {
		return nil
	}
	return &ProxyInfo{
		SourceAddr: parts[2],
		DestAddr:   parts[3],
		SourcePort: srcPort,
		DestPort:   dstPort,
		Family:     family,
	}
}

// ipv6String 把 16 字节 IPv6 地址格式化为规范字符串（如 "::1"）。
// 比原版 TS 的逐组 16 进制拼接（"0:0:0:0:0:0:0:1"）更规范——这是 Go 更好的实现。
func ipv6String(b []byte) string {
	ip := make(net.IP, net.IPv6len)
	copy(ip, b)
	return ip.String()
}
