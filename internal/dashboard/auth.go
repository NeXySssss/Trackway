package dashboard

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

type authManager struct {
	mu         sync.Mutex
	tokenTTL   time.Duration
	sessionTTL time.Duration
	tokens     map[string]time.Time
	sessions   map[string]time.Time
}

func newAuthManager(tokenTTL, sessionTTL time.Duration) *authManager {
	return &authManager{
		tokenTTL:   tokenTTL,
		sessionTTL: sessionTTL,
		tokens:     make(map[string]time.Time),
		sessions:   make(map[string]time.Time),
	}
}

func (m *authManager) IssueToken(now time.Time) (string, error) {
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanup(now)
	m.tokens[token] = now.Add(m.tokenTTL)
	return token, nil
}

func (m *authManager) ConsumeToken(now time.Time, token string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanup(now)

	expiresAt, ok := m.tokens[token]
	if !ok || now.After(expiresAt) {
		delete(m.tokens, token)
		return "", false
	}
	delete(m.tokens, token)

	sessionID, err := m.createSessionLocked(now)
	if err != nil {
		return "", false
	}
	return sessionID, true
}

func (m *authManager) CreateSession(now time.Time) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanup(now)
	return m.createSessionLocked(now)
}

func (m *authManager) Session(now time.Time, sessionID string) (time.Time, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanup(now)

	startedAt, ok := m.sessions[sessionID]
	if !ok {
		return time.Time{}, false
	}
	expiresAt := startedAt.Add(m.sessionTTL)
	if now.After(expiresAt) {
		delete(m.sessions, sessionID)
		return time.Time{}, false
	}
	return expiresAt, true
}

func (m *authManager) RevokeSession(sessionID string) {
	if sessionID == "" {
		return
	}
	m.mu.Lock()
	delete(m.sessions, sessionID)
	m.mu.Unlock()
}

func (m *authManager) cleanup(now time.Time) {
	for token, expiresAt := range m.tokens {
		if now.After(expiresAt) {
			delete(m.tokens, token)
		}
	}
	for sessionID, startedAt := range m.sessions {
		if now.After(startedAt.Add(m.sessionTTL)) {
			delete(m.sessions, sessionID)
		}
	}
}

func (m *authManager) createSessionLocked(now time.Time) (string, error) {
	sessionID, err := randomToken(32)
	if err != nil {
		return "", err
	}
	m.sessions[sessionID] = now
	return sessionID, nil
}

func randomToken(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
