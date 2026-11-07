package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestShutdownHTTPServerCancelsRequestsAfterDeadline(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	srv := &http.Server{
		ReadHeaderTimeout: time.Second,
		Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			close(started)
			<-r.Context().Done()
			close(canceled)
		}),
	}
	address := serveShutdownTest(t, srv)
	requestDone := make(chan error, 1)
	go func() {
		response, err := http.Get(address)
		if err == nil {
			_ = response.Body.Close()
		}
		requestDone <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not reach the server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := shutdownHTTPServer(ctx, srv); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want deadline exceeded", err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("request context survived shutdown deadline; a database query could keep the pool open")
	}
	select {
	case err := <-requestDone:
		if err == nil {
			t.Fatal("unfinished request unexpectedly completed successfully")
		}
	case <-time.After(time.Second):
		t.Fatal("client connection remained open after shutdown deadline")
	}
}

func TestShutdownHTTPServerAllowsRequestsToFinish(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	srv := &http.Server{
		ReadHeaderTimeout: time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(started)
			select {
			case <-release:
				_, _ = io.WriteString(w, "finished")
			case <-r.Context().Done():
			}
		}),
	}
	address := serveShutdownTest(t, srv)
	requestDone := make(chan string, 1)
	go func() {
		response, err := http.Get(address)
		if err != nil {
			requestDone <- err.Error()
			return
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		requestDone <- string(body)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not reach the server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- shutdownHTTPServer(ctx, srv) }()
	close(release)
	if err := <-shutdownDone; err != nil {
		t.Fatalf("graceful shutdown: %v", err)
	}
	if body := <-requestDone; body != "finished" {
		t.Fatalf("response = %q, want completed request", body)
	}
}

func serveShutdownTest(t *testing.T, srv *http.Server) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(listener) }()
	t.Cleanup(func() {
		_ = srv.Close()
		if err := <-serveDone; !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("serve: %v", err)
		}
	})
	return "http://" + listener.Addr().String()
}
