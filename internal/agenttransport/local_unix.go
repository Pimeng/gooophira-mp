//go:build aix || android || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package agenttransport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func defaultUnixPath(instance string) string {
	return filepath.Join(os.TempDir(), "pma-"+instance+".sock")
}

func listenLocal(endpoint Endpoint) (*Listener, error) {
	if endpoint.Scheme != SchemeUnix {
		return nil, fmt.Errorf("agent transport: %s is not supported on this platform", endpoint.Scheme)
	}
	path := endpoint.Address
	if len(path) > 100 {
		return nil, errors.New("agent transport: unix socket path is too long")
	}
	if err := prepareUnixSocket(path); err != nil {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("agent transport: listen unix: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("agent transport: chmod unix socket: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	cleanup := func() error {
		current, statErr := os.Lstat(path)
		if errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
		if statErr != nil || !os.SameFile(info, current) {
			return statErr
		}
		return os.Remove(path)
	}
	return &Listener{Listener: ln, Endpoint: endpoint, cleanup: cleanup}, nil
}

func prepareUnixSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(filepath.Dir(path), 0o700)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return errors.New("agent transport: unix endpoint exists and is not a socket")
	}
	conn, dialErr := net.DialTimeout("unix", path, 150*time.Millisecond)
	if dialErr == nil {
		_ = conn.Close()
		return errors.New("agent transport: unix endpoint is already in use")
	}
	var errno syscall.Errno
	if !errors.As(dialErr, &errno) || (errno != syscall.ECONNREFUSED && errno != syscall.ENOENT) {
		return fmt.Errorf("agent transport: cannot verify stale unix socket: %w", dialErr)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("agent transport: remove stale unix socket: %w", err)
	}
	return nil
}

func dialLocal(ctx context.Context, endpoint Endpoint) (net.Conn, error) {
	if endpoint.Scheme != SchemeUnix {
		return nil, fmt.Errorf("agent transport: %s is not supported on this platform", endpoint.Scheme)
	}
	return (&net.Dialer{}).DialContext(ctx, "unix", endpoint.Address)
}
