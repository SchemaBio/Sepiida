package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestNewHTTPServerAppliesConnectionLimits(t *testing.T) {
	server := newHTTPServer("127.0.0.1:0", http.NewServeMux())
	if server.ReadHeaderTimeout != 5*time.Second || server.ReadTimeout != 30*time.Second || server.WriteTimeout != 30*time.Second {
		t.Fatalf("unexpected request timeouts: header=%s read=%s write=%s", server.ReadHeaderTimeout, server.ReadTimeout, server.WriteTimeout)
	}
	if server.IdleTimeout != 120*time.Second || server.MaxHeaderBytes != 32<<10 {
		t.Fatalf("unexpected connection limits: idle=%s headers=%d", server.IdleTimeout, server.MaxHeaderBytes)
	}
}

func TestServeHTTPServerWaitsForActiveRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, "ok")
	})
	server := newHTTPServer("127.0.0.1:0", handler)
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- serveHTTPServer(ctx, server, listener) }()

	requestDone := make(chan error, 1)
	go func() {
		response, requestErr := http.Get("http://" + listener.Addr().String())
		if requestErr == nil {
			requestErr = response.Body.Close()
		}
		requestDone <- requestErr
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not reach handler")
	}
	cancel()
	select {
	case err := <-serveDone:
		t.Fatalf("server stopped before active request completed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-requestDone:
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request did not complete")
	}
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("serveHTTPServer: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not shut down")
	}
}
