//go:build windows

package agenttransport

import (
	"context"
	"fmt"
	"net"
	"os/user"
	"path/filepath"

	winio "github.com/Microsoft/go-winio"
)

func defaultUnixPath(instance string) string { return "" }

func listenLocal(endpoint Endpoint) (*Listener, error) {
	if endpoint.Scheme != SchemeNPipe {
		return nil, fmt.Errorf("agent transport: %s is not supported on Windows", endpoint.Scheme)
	}
	current, err := user.Current()
	if err != nil || current.Uid == "" {
		return nil, fmt.Errorf("agent transport: resolve current user SID: %w", err)
	}
	path := pipePath(endpoint.Address)
	acl := "D:P(A;;GA;;;" + current.Uid + ")"
	ln, err := winio.ListenPipe(path, &winio.PipeConfig{
		SecurityDescriptor: acl,
		MessageMode:        false,
		InputBufferSize:    64 * 1024,
		OutputBufferSize:   64 * 1024,
	})
	if err != nil {
		return nil, fmt.Errorf("agent transport: listen named pipe: %w", err)
	}
	return &Listener{Listener: ln, Endpoint: endpoint}, nil
}

func dialLocal(ctx context.Context, endpoint Endpoint) (net.Conn, error) {
	if endpoint.Scheme != SchemeNPipe {
		return nil, fmt.Errorf("agent transport: %s is not supported on Windows", endpoint.Scheme)
	}
	return winio.DialPipeContext(ctx, pipePath(endpoint.Address))
}

func pipePath(name string) string {
	return `\\.\pipe\` + filepath.Base(name)
}
