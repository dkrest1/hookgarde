package server

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestGracefulDrain(t *testing.T) {
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-release; w.WriteHeader(204) }))
	}()
	requestDone := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 3 * time.Second}
		response, e := client.Get("http://" + l.Addr().String())
		if e == nil {
			response.Body.Close()
			if response.StatusCode != 204 {
				t.Errorf("response=%d", response.StatusCode)
			}
		}
		requestDone <- e
	}()
	<-started
	cancel()
	select {
	case e := <-done:
		t.Fatalf("server did not drain: %v", e)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if e = <-requestDone; e != nil {
		t.Fatal(e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}
