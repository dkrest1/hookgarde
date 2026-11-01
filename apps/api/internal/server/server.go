// Package server implements bounded HTTP serving and graceful shutdown.
package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

func Run(ctx context.Context, address string, h http.Handler) error {
	listener, e := net.Listen("tcp", address)
	if e != nil {
		return e
	}
	return Serve(ctx, listener, h)
}
func Serve(ctx context.Context, l net.Listener, h http.Handler) error {
	s := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 * 1024}
	done := make(chan error, 1)
	go func() { done <- s.Serve(l) }()
	select {
	case e := <-done:
		if errors.Is(e, http.ErrServerClosed) {
			return nil
		}
		return e
	case <-ctx.Done():
		shutdown, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if e := s.Shutdown(shutdown); e != nil {
			_ = s.Close()
			return e
		}
		e := <-done
		if errors.Is(e, http.ErrServerClosed) {
			return nil
		}
		return e
	}
}
