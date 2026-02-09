package dashboard

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"trackway/internal/config"
	"trackway/internal/logstore"
	"trackway/internal/tracker"
	"trackway/internal/util"
)

const (
	sessionCookieName = "trackway_dashboard_session"
	sessionMaxAge     = 24 * time.Hour
)

//go:embed all:frontend/dist
var staticFiles embed.FS

type DataProvider interface {
	Snapshot() tracker.Snapshot
	Logs(trackName string, days int, limit int) ([]logstore.Row, bool)
}

type Server struct {
	logger       *slog.Logger
	provider     DataProvider
	auth         *authManager
	miniApp      *miniAppVerifier
	miniAppOn    bool
	listenAddr   string
	publicURL    string
	secureCookie bool
	static       fs.FS
	httpServer   *http.Server
}

func New(cfg config.Dashboard, botToken string, provider DataProvider) (*Server, error) {
	if provider == nil {
		return nil, errors.New("dashboard data provider is required")
	}

	staticFS, err := fs.Sub(staticFiles, "frontend/dist")
	if err != nil {
		return nil, err
	}

	tokenTTL := time.Duration(cfg.AuthTokenTTLSeconds) * time.Second
	if tokenTTL <= 0 {
		tokenTTL = 5 * time.Minute
	}

	srv := &Server{
		logger:       slog.Default(),
		provider:     provider,
		auth:         newAuthManager(tokenTTL, sessionMaxAge),
		miniApp:      newMiniAppVerifier(botToken, time.Duration(cfg.MiniAppMaxAgeSec)*time.Second),
		miniAppOn:    cfg.MiniAppEnabled,
		listenAddr:   cfg.ListenAddress,
		publicURL:    strings.TrimRight(cfg.PublicURL, "/"),
		secureCookie: cfg.SecureCookie,
		static:       staticFS,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", srv.handleHealth)
	mux.HandleFunc("/auth/verify", srv.handleAuthVerify)
	mux.HandleFunc("/auth/logout", srv.handleAuthLogout)
	mux.HandleFunc("/api/auth/session", srv.handleAuthSession)
	mux.HandleFunc("/api/auth/telegram-miniapp", srv.handleTelegramMiniAppAuth)
	mux.HandleFunc("/api/status", srv.requireAuth(srv.handleStatus))
	mux.HandleFunc("/api/logs", srv.requireAuth(srv.handleLogs))
	mux.Handle("/", srv.staticHandler())

	srv.httpServer = &http.Server{
		Addr:    srv.listenAddr,
		Handler: mux,
	}
	return srv, nil
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = s.httpServer.Shutdown(shutdownCtx)
		case <-stop:
			return
		}
	}()
	defer close(stop)

	s.logger.Info("dashboard listening", "addr", s.listenAddr)
	err := s.httpServer.ListenAndServe()
	if err == nil {
		return nil
	}
	if errors.Is(err, http.ErrServerClosed) && ctx.Err() != nil {
		return nil
	}
	return err
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"time": time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) NewAuthLink() (string, error) {
	if s.publicURL == "" {
		return "", errors.New("dashboard.public_url is empty")
	}
	token, err := s.auth.IssueToken(time.Now().UTC())
	if err != nil {
		return "", err
	}

	link, err := url.Parse(s.publicURL + "/auth/verify")
	if err != nil {
		return "", err
	}
	q := link.Query()
	q.Set("token", token)
	link.RawQuery = q.Encode()
	return link.String(), nil
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC()
		sessionID, ok := s.sessionIDFromRequest(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"authorized": false,
				"error":      "not authorized",
			})
			return
		}
		expiresAt, ok := s.auth.Session(now, sessionID)
		if !ok {
			s.expireCookie(w)
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"authorized": false,
				"error":      "session expired",
			})
			return
		}
		w.Header().Set("X-Session-Expires-At", expiresAt.Format(time.RFC3339))
		next(w, r)
	}
}

func (s *Server) handleAuthVerify(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.FormValue("token"))
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.renderVerifyPage(w, token)
		return
	case http.MethodPost:
		// proceed to token consumption
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	now := time.Now().UTC()
	sessionID, ok := s.auth.ConsumeToken(now, token)
	if !ok {
		http.Error(w, "token is invalid or expired", http.StatusUnauthorized)
		return
	}

	s.setSessionCookie(w, sessionID)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) renderVerifyPage(w http.ResponseWriter, token string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(
		w,
		"<!doctype html><html><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">"+
			"<title>Trackway Auth</title><style>body{font-family:Arial,sans-serif;background:#0f1720;color:#e7f0f5;margin:0}"+
			".card{max-width:520px;margin:8vh auto;background:#162532;border:1px solid #2e4a5b;border-radius:12px;padding:20px}"+
			"h1{font-size:20px;margin:0 0 12px}p{color:#a7beca}button{background:#2093c3;color:white;border:0;padding:10px 14px;border-radius:8px;cursor:pointer}"+
			"code{background:#10202d;border:1px solid #2e4a5b;padding:2px 6px;border-radius:6px}</style></head><body>"+
			"<main class=\"card\"><h1>Authorize dashboard session</h1><p>Press the button below in the same browser where you will open dashboard.</p>"+
			"<form method=\"post\" action=\"/auth/verify\"><input type=\"hidden\" name=\"token\" value=\"%s\"><button type=\"submit\">Authorize this browser</button></form>"+
			"<p>Token is one-time and expires quickly.</p><p>If this page was opened by a link preview bot, just ignore it and open the link manually.</p>"+
			"</main></body></html>",
		util.HTMLEscape(token),
	)
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := s.sessionIDFromRequest(r)
	if ok {
		s.auth.RevokeSession(sessionID)
	}
	s.expireCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true,
	})
}

func (s *Server) handleAuthSession(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	sessionID, ok := s.sessionIDFromRequest(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"authorized":       false,
			"mini_app_enabled": s.miniAppOn && s.miniApp != nil,
		})
		return
	}

	expiresAt, ok := s.auth.Session(now, sessionID)
	if !ok {
		s.expireCookie(w)
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"authorized":       false,
			"mini_app_enabled": s.miniAppOn && s.miniApp != nil,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"authorized":       true,
		"expires_at":       expiresAt.Format(time.RFC3339),
		"mini_app_enabled": s.miniAppOn && s.miniApp != nil,
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	snapshot := s.provider.Snapshot()
	targets := make([]map[string]any, 0, len(snapshot.Targets))
	for _, target := range snapshot.Targets {
		targets = append(targets, map[string]any{
			"name":         target.Name,
			"address":      target.Address,
			"port":         target.Port,
			"status":       target.Status,
			"last_changed": util.FormatTime(target.LastChanged),
			"last_checked": util.FormatTime(target.LastChecked),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"generated_at": snapshot.GeneratedAt.Format(time.RFC3339),
		"total":        snapshot.Total,
		"up":           snapshot.Up,
		"down":         snapshot.Down,
		"unknown":      snapshot.Unknown,
		"targets":      targets,
	})
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	track := strings.TrimSpace(r.URL.Query().Get("track"))
	if track == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "track is required",
		})
		return
	}

	days := parseQueryInt(r, "days", 7, 1, 365)
	hours := parseQueryInt(r, "hours", 0, 0, 24*365)
	limit := parseQueryInt(r, "limit", 5000, 1, 50000)
	if hours > 0 {
		roundedDays := (hours + 23) / 24
		if roundedDays > days {
			days = roundedDays
		}
	}
	rows, ok := s.provider.Logs(track, days, limit)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": "track not found",
		})
		return
	}
	if hours > 0 {
		cutoff := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)
		rows = filterRowsByCutoff(rows, cutoff)
		if len(rows) > limit {
			rows = rows[len(rows)-limit:]
		}
	}

	zone := parseClientZone(r)

	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, formatRowLine(row, zone))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"track":  track,
		"days":   days,
		"hours":  hours,
		"limit":  limit,
		"rows":   rows,
		"text":   strings.Join(lines, "\n"),
		"format": "DD.MM.YYYY HH:mm:ss",
	})
}

func (s *Server) handleTelegramMiniAppAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.miniAppOn || s.miniApp == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "mini app auth is disabled",
		})
		return
	}

	var payload struct {
		InitData string `json:"init_data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "invalid json body",
		})
		return
	}
	user, err := s.miniApp.Verify(payload.InitData, time.Now().UTC())
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": err.Error(),
		})
		return
	}

	sessionID, issueErr := s.auth.CreateSession(time.Now().UTC())
	if issueErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "failed to create auth session",
		})
		return
	}

	s.setSessionCookie(w, sessionID)
	writeJSON(w, http.StatusOK, map[string]any{
		"authorized": true,
		"user_id":    user.ID,
	})
}

func parseQueryInt(r *http.Request, key string, fallback, min, max int) int {
	value := strings.TrimSpace(r.URL.Query().Get(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	if parsed < min {
		return min
	}
	if parsed > max {
		return max
	}
	return parsed
}

func (s *Server) sessionIDFromRequest(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return "", false
	}
	value := strings.TrimSpace(cookie.Value)
	if value == "" {
		return "", false
	}
	return value, true
}

func (s *Server) setSessionCookie(w http.ResponseWriter, sessionID string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCookie,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) expireCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCookie,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}

func (s *Server) staticHandler() http.Handler {
	fileServer := http.FileServer(http.FS(s.static))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/auth/") {
			http.NotFound(w, r)
			return
		}

		cleanPath := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if cleanPath == "." || cleanPath == "/" {
			cleanPath = "index.html"
		}
		if _, err := fs.Stat(s.static, cleanPath); err != nil {
			cleanPath = "index.html"
		}
		if cleanPath == "index.html" {
			indexBytes, err := fs.ReadFile(s.static, "index.html")
			if err != nil {
				http.Error(w, "dashboard index not found", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(indexBytes)
			return
		}

		r2 := r.Clone(r.Context())
		r2.URL.Path = "/" + cleanPath
		fileServer.ServeHTTP(w, r2)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func filterRowsByCutoff(rows []logstore.Row, cutoff time.Time) []logstore.Row {
	if len(rows) == 0 {
		return rows
	}
	out := make([]logstore.Row, 0, len(rows))
	for _, row := range rows {
		ts, err := time.Parse(time.RFC3339, row.Timestamp)
		if err != nil {
			continue
		}
		if ts.Before(cutoff) {
			continue
		}
		out = append(out, row)
	}
	return out
}

func parseClientZone(r *http.Request) *time.Location {
	offsetMin := parseQueryInt(r, "tz_offset_minutes", 0, -14*60, 14*60)
	return time.FixedZone("client", offsetMin*60)
}

func formatRowLine(row logstore.Row, loc *time.Location) string {
	timestamp := row.Timestamp
	ts, err := time.Parse(time.RFC3339, row.Timestamp)
	if err == nil {
		timestamp = ts.In(loc).Format("02.01.2006 15:04:05")
	}
	return timestamp + "  " + row.Status + "  " + row.Endpoint + "  " + row.Reason
}
