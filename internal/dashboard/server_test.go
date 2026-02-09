package dashboard

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"trackway/internal/config"
	"trackway/internal/logstore"
	"trackway/internal/tracker"
)

type stubProvider struct{}

func (stubProvider) Snapshot() tracker.Snapshot {
	return tracker.Snapshot{}
}

func (stubProvider) Logs(string, int, int) ([]logstore.Row, bool) {
	return nil, false
}

func TestStaticHandlerServesIndexWithoutRedirect(t *testing.T) {
	t.Parallel()

	srv, err := New(config.Dashboard{
		ListenAddress: ":0",
		PublicURL:     "http://127.0.0.1:8080",
	}, stubProvider{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "" {
		t.Fatalf("unexpected redirect location: %q", got)
	}
	if body := rec.Body.String(); !strings.Contains(strings.ToLower(body), "<!doctype html>") {
		t.Fatalf("expected html document, got %q", body)
	}
}

func TestStaticHandlerServesIndexForUnknownPath(t *testing.T) {
	t.Parallel()

	srv, err := New(config.Dashboard{
		ListenAddress: ":0",
		PublicURL:     "http://127.0.0.1:8080",
	}, stubProvider{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/this-path-does-not-exist", nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "" {
		t.Fatalf("unexpected redirect location: %q", got)
	}
}

func TestHealthEndpoint(t *testing.T) {
	t.Parallel()

	srv, err := New(config.Dashboard{
		ListenAddress: ":0",
		PublicURL:     "http://127.0.0.1:8080",
	}, stubProvider{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestListenAndServeReturnsStartupError(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	srv, err := New(config.Dashboard{
		ListenAddress: listener.Addr().String(),
		PublicURL:     "http://127.0.0.1:8080",
	}, stubProvider{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- srv.ListenAndServe(ctx)
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected startup error, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ListenAndServe did not return on startup error")
	}
}
