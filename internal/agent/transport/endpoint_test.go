package agenttransport

import (
	"context"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestParseEndpoints(t *testing.T) {
	tests := []struct {
		raw    string
		scheme Scheme
		valid  bool
	}{
		{raw: "disabled", scheme: SchemeDisabled, valid: true},
		{raw: "auto", scheme: SchemeAuto, valid: true},
		{raw: "unix:///tmp/phira.sock", scheme: SchemeUnix, valid: true},
		{raw: "npipe://./pipe/phira-mp-agent", scheme: SchemeNPipe, valid: true},
		{raw: "tcp://127.0.0.1:1234", scheme: SchemeTCP, valid: true},
		{raw: "tcp://[::1]:1234", scheme: SchemeTCP, valid: true},
		{raw: "tcp://0.0.0.0:1234", valid: false},
		{raw: "tcp://localhost:1234", valid: false},
		{raw: "udp://127.0.0.1:1234", valid: false},
	}
	for _, test := range tests {
		t.Run(test.raw, func(t *testing.T) {
			endpoint, err := Parse(test.raw)
			if test.valid && err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !test.valid && err == nil {
				t.Fatalf("Parse unexpectedly accepted %+v", endpoint)
			}
			if test.valid && endpoint.Scheme != test.scheme {
				t.Fatalf("scheme = %s, want %s", endpoint.Scheme, test.scheme)
			}
		})
	}
}

func TestTCPListenerIsLoopbackAndDialable(t *testing.T) {
	endpoint, err := Parse("tcp://127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := Listen(endpoint, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	host, _, err := net.SplitHostPort(listener.Endpoint.Address)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		t.Fatalf("listener address is not loopback: %s", listener.Endpoint.Address)
	}
	done := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			_ = conn.Close()
		}
		done <- acceptErr
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := DialContext(ctx, listener.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestExplicitUnsupportedLocalTransportDoesNotFallback(t *testing.T) {
	var raw string
	if runtime.GOOS == "windows" {
		raw = "unix:///tmp/phira-test.sock"
	} else {
		raw = "npipe://./pipe/phira-test"
	}
	endpoint, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Listen(endpoint, "test")
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("explicit unsupported transport error = %v", err)
	}
}
