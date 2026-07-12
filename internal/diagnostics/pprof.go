package diagnostics

import (
	"context"
	"net"
	"net/http"
	_ "net/http/pprof"
)

type Service struct {
	listener net.Listener
	server   *http.Server
}

func Start() (*Service, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	service := &Service{
		listener: listener,
		server:   &http.Server{Handler: http.DefaultServeMux},
	}
	go service.server.Serve(listener)
	return service, nil
}

func (s *Service) URL() string {
	return "http://" + s.listener.Addr().String() + "/debug/pprof/"
}

func (s *Service) Close(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}
