// Package netutil 提供跨包复用的 HTTP / Transport 工厂，集中处理 Termux/Android
// 等无本地 stub resolver 环境下的 [::1]:53 / 127.0.0.1:53 connection refused 问题。
//
// 设计原则：仅在 Android 平台（runtime.GOOS == "android"，含 Termux）启用自定义
// Resolver（公共 DNS 1.1.1.1 / 8.8.8.8）以绕开本机回环 DNS；其它平台
// （Linux 服务器 / macOS / Windows）保留 net/http 默认行为，继续走系统 resolver，
// 以保证 /etc/hosts、内网 DNS、配置 nameserver 正常工作，且不引入额外延迟与隐私泄漏。
//
// 调用方一律经 NewClient / NewTransport 构造 HTTP 客户端，禁止直接使用
// &http.Client{} 或裸 &http.Transport{}，便于后续统一调整。
package netutil

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"sync"
	"time"
)

// 默认网络参数：所有出站 HTTP 共享一组配置，便于排查与测试。
const (
	// DialTimeout 单次 DNS / TCP 拨号超时。
	DialTimeout = 10 * time.Second
	// KeepAlive TCP keep-alive 间隔。
	KeepAlive = 30 * time.Second
	// IdleConnTimeout 空闲连接保持时间。
	IdleConnTimeout = 90 * time.Second
	// TLSHandshakeTimeout TLS 握手超时。
	TLSHandshakeTimeout = 10 * time.Second
	// ExpectContinueTimeout 100-continue 等待超时。
	ExpectContinueTimeout = 1 * time.Second
	// MaxIdleConns 连接池最大空闲连接数。
	MaxIdleConns = 100
)

// defaultDNSServers 是 Android 平台专用公共 DNS 解析端点列表的内置默认。
var defaultDNSServers = []string{"1.1.1.1:53", "8.8.8.8:53"}

// dnsServers 是当前生效的 Android 平台公共 DNS 解析端点列表，按顺序回退。
// 用 var 而非 const 便于测试调整与服务启动时从配置文件加载。
var (
	dnsServers = append([]string(nil), defaultDNSServers...)
	dnsMu      sync.RWMutex

	// proxyURL 是当前生效的出站代理 URL（空 = 未配置，走环境变量）。
	// 优先级：环境变量（HTTP_PROXY/HTTPS_PROXY）> 配置文件 > 直连。
	proxyURL    string
	proxyDirect bool
	proxyMu     sync.RWMutex
)

// SetDNSServers 设置 Android 平台公共 DNS 服务器列表（含端口，如 "1.1.1.1:53"）。
// 空列表会被忽略。通常在服务启动时从配置文件读取后调用一次；已创建的 http.Transport
// 的 Resolver 闭包会读取最新列表，因此新请求无需重启即可生效。
func SetDNSServers(servers []string) {
	if len(servers) == 0 {
		return
	}
	dnsMu.Lock()
	defer dnsMu.Unlock()
	dnsServers = append([]string(nil), servers...)
}

func currentDNSServers() []string {
	dnsMu.RLock()
	defer dnsMu.RUnlock()
	return append([]string(nil), dnsServers...)
}

// SetProxy 设置出站代理 URL（如 "http://127.0.0.1:7890"）。
// direct 为 true 时强制直连；空字符串表示未配置（走环境变量代理）。
// 通常在服务启动时从配置文件读取后调用一次。
func SetProxy(url string, direct bool) {
	proxyMu.Lock()
	defer proxyMu.Unlock()
	proxyURL = url
	proxyDirect = direct
}

func currentProxy() (string, bool) {
	proxyMu.RLock()
	defer proxyMu.RUnlock()
	return proxyURL, proxyDirect
}

// proxyFunc 返回 http.ProxyURL 函数，实现优先级：环境变量 > 配置文件 > 直连。
func proxyFunc() func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		configured, direct := currentProxy()
		if direct {
			return nil, nil
		}
		// 优先级 1：环境变量（HTTP_PROXY/HTTPS_PROXY/NO_PROXY）
		if u, err := http.ProxyFromEnvironment(req); err == nil && u != nil {
			return u, nil
		}
		// 优先级 2：配置文件
		if configured != "" {
			return url.Parse(configured)
		}
		// 优先级 3：直连
		return nil, nil
	}
}

// PublicResolver 返回绕过本地 stub resolver 的 net.Resolver。
// 仅在 Android 平台使用，其它平台应直接用 nil 走系统 resolver。
func PublicResolver() *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: DialTimeout}
			var lastErr error
			for _, addr := range currentDNSServers() {
				conn, err := d.DialContext(ctx, network, addr)
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
	}
}

// dialContext 返回适合当前平台的 DialContext：Android 用公共 DNS 解析器，
// 其它平台用 nil（走系统默认 resolver，保留 /etc/hosts、内网 DNS 等行为）。
func dialContext() func(ctx context.Context, network, addr string) (net.Conn, error) {
	if runtime.GOOS != "android" {
		return nil
	}
	d := &net.Dialer{Timeout: DialTimeout, KeepAlive: KeepAlive, Resolver: PublicResolver()}
	return d.DialContext
}

// NewTransport 返回带平台适配的 http.Transport。
//   - Android：注入自定义 DialContext（公共 DNS 解析），绕开 [::1]:53 / 127.0.0.1:53
//     connection refused（Termux 无本地 stub resolver）。
//   - 其它平台：不设 DialContext，使用 net/http 默认 resolver（系统 /etc/resolv.conf）。
//
// 已配置 ForceAttemptHTTP2 / 连接池与常见超时；Proxy 按优先级：环境变量 > 配置文件 > 直连。
func NewTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 proxyFunc(),
		DialContext:           dialContext(),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          MaxIdleConns,
		IdleConnTimeout:       IdleConnTimeout,
		TLSHandshakeTimeout:   TLSHandshakeTimeout,
		ExpectContinueTimeout: ExpectContinueTimeout,
	}
}

// NewClient 返回平台适配的 http.Client。
// 不设 Timeout：超时由调用方按需通过 context 或 client.Timeout 控制。
func NewClient() *http.Client {
	return &http.Client{Transport: NewTransport()}
}
