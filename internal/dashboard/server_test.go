package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
