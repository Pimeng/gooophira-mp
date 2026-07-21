//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package agenttransport

import (
	"context"
	"fmt"
	"net"
)

func defaultUnixPath(instance string) string { return "" }

func listenLocal(endpoint Endpoint) (*Listener, error) {
	return nil, fmt.Errorf("agent transport: %s is not supported on this platform", endpoint.Scheme)
}

func dialLocal(ctx context.Context, endpoint Endpoint) (net.Conn, error) {
	return nil, fmt.Errorf("agent transport: %s is not supported on this platform", endpoint.Scheme)
}
