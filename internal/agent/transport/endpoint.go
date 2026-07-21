// agenttransport 包为 Agent HTTP 协议提供平台本地监听器和拨号器。
package agenttransport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

type Scheme string

const (
	SchemeDisabled Scheme = "disabled"
	SchemeAuto     Scheme = "auto"
	SchemeUnix     Scheme = "unix"
	SchemeNPipe    Scheme = "npipe"
	SchemeTCP      Scheme = "tcp"
)

type Endpoint struct {
	Scheme  Scheme
	Address string
}

func Parse(raw string) (Endpoint, error) {
	raw = strings.TrimSpace(raw)
	switch raw {
	case "", "disabled":
		return Endpoint{Scheme: SchemeDisabled}, nil
	case "auto":
		return Endpoint{Scheme: SchemeAuto}, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return Endpoint{}, fmt.Errorf("agent transport: parse endpoint: %w", err)
	}
	switch Scheme(strings.ToLower(u.Scheme)) {
	case SchemeUnix:
		if u.Path == "" || u.Host != "" {
			return Endpoint{}, errors.New("agent transport: unix endpoint requires an absolute path")
		}
		return Endpoint{Scheme: SchemeUnix, Address: filepath.Clean(u.Path)}, nil
	case SchemeNPipe:
		address := strings.TrimPrefix(u.Host+u.Path, "./pipe/")
		address = strings.TrimPrefix(address, "/pipe/")
		address = strings.Trim(address, "/\\")
		if address == "" || strings.Contains(address, "..") {
			return Endpoint{}, errors.New("agent transport: invalid named pipe endpoint")
		}
		return Endpoint{Scheme: SchemeNPipe, Address: address}, nil
	case SchemeTCP:
		if u.Host == "" || u.Path != "" {
			return Endpoint{}, errors.New("agent transport: tcp endpoint requires host:port")
		}
		if err := validateLoopbackAddress(u.Host); err != nil {
			return Endpoint{}, err
		}
		return Endpoint{Scheme: SchemeTCP, Address: u.Host}, nil
	default:
		return Endpoint{}, fmt.Errorf("agent transport: unsupported endpoint %q", raw)
	}
}

func (e Endpoint) String() string {
	switch e.Scheme {
	case SchemeDisabled, SchemeAuto:
		return string(e.Scheme)
	case SchemeUnix:
		return "unix://" + filepath.ToSlash(e.Address)
	case SchemeNPipe:
		return "npipe://./pipe/" + e.Address
	case SchemeTCP:
		return "tcp://" + e.Address
	default:
		return ""
	}
}

type Listener struct {
	net.Listener
	Endpoint Endpoint
	cleanup  func() error
}

func (l *Listener) Close() error {
	err := l.Listener.Close()
	if l.cleanup != nil {
		if cleanupErr := l.cleanup(); err == nil {
			err = cleanupErr
		}
	}
	return err
}

// Listen 使用请求的传输方式；只有 auto 模式可以回退到 TCP。
func Listen(endpoint Endpoint, instance string) (*Listener, error) {
	if endpoint.Scheme == SchemeDisabled {
		return nil, errors.New("agent transport: endpoint is disabled")
	}
	if endpoint.Scheme != SchemeAuto {
		return listenExact(endpoint)
	}
	local := defaultLocalEndpoint(sanitizeInstance(instance))
	listener, localErr := listenExact(local)
	if localErr == nil {
		return listener, nil
	}
	fallback, tcpErr := listenTCP(Endpoint{Scheme: SchemeTCP, Address: "127.0.0.1:0"})
	if tcpErr != nil {
		return nil, fmt.Errorf("agent transport: local IPC failed (%v), TCP fallback failed: %w", localErr, tcpErr)
	}
	return fallback, nil
}

func listenExact(endpoint Endpoint) (*Listener, error) {
	switch endpoint.Scheme {
	case SchemeUnix, SchemeNPipe:
		return listenLocal(endpoint)
	case SchemeTCP:
		return listenTCP(endpoint)
	default:
		return nil, fmt.Errorf("agent transport: cannot listen on %s", endpoint.Scheme)
	}
}

func listenTCP(endpoint Endpoint) (*Listener, error) {
	if err := validateLoopbackAddress(endpoint.Address); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", endpoint.Address)
	if err != nil {
		return nil, fmt.Errorf("agent transport: listen TCP: %w", err)
	}
	actual := Endpoint{Scheme: SchemeTCP, Address: ln.Addr().String()}
	return &Listener{Listener: ln, Endpoint: actual}, nil
}

func DialContext(ctx context.Context, endpoint Endpoint) (net.Conn, error) {
	switch endpoint.Scheme {
	case SchemeUnix, SchemeNPipe:
		return dialLocal(ctx, endpoint)
	case SchemeTCP:
		if err := validateLoopbackAddress(endpoint.Address); err != nil {
			return nil, err
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", endpoint.Address)
	default:
		return nil, fmt.Errorf("agent transport: cannot dial %s", endpoint.Scheme)
	}
}

func validateLoopbackAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("agent transport: invalid TCP address: %w", err)
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil || !ip.IsLoopback() {
		return errors.New("agent transport: TCP endpoint must use a literal loopback address")
	}
	return nil
}

func sanitizeInstance(instance string) string {
	instance = strings.TrimSpace(instance)
	if instance == "" {
		return "default"
	}
	var b strings.Builder
	for _, r := range instance {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "default"
	}
	return b.String()
}

func defaultLocalEndpoint(instance string) Endpoint {
	if runtime.GOOS == "windows" {
		return Endpoint{Scheme: SchemeNPipe, Address: "phira-mp-agent-" + instance}
	}
	return Endpoint{Scheme: SchemeUnix, Address: defaultUnixPath(instance)}
}
