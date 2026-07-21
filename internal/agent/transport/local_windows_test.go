//go:build windows

package agenttransport

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestNamedPipeListenAndDial(t *testing.T) {
	endpoint, err := Parse(fmt.Sprintf("npipe://./pipe/phira-mp-agent-test-%d", os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := Listen(endpoint, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	accepted := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			_ = conn.Close()
		}
		accepted <- acceptErr
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := DialContext(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if err := <-accepted; err != nil {
		t.Fatal(err)
	}
}
