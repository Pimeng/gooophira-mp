//go:build aix || android || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package agenttransport

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUnixSocketPermissionsAndCleanup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.sock")
	endpoint, err := Parse("unix://" + filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := Listen(endpoint, "test")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %o, want 600", info.Mode().Perm())
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("socket remains after close: %v", err)
	}
}

func TestUnixSocketRefusesActiveAndReclaimsStaleEndpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.sock")
	active, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := Endpoint{Scheme: SchemeUnix, Address: path}
	if _, err := Listen(endpoint, "test"); err == nil {
		active.Close()
		t.Fatal("active Unix socket was replaced")
	}
	if err := active.Close(); err != nil {
		t.Fatal(err)
	}
	listener, err := Listen(endpoint, "test")
	if err != nil {
		t.Fatalf("stale Unix socket was not reclaimed: %v", err)
	}
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := DialContext(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
}
