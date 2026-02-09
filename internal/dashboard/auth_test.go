package dashboard

import (
	"testing"
	"time"
)

func TestAuthManagerTokenAndSessionLifecycle(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	manager := newAuthManager(2*time.Minute, 24*time.Hour)

	token, err := manager.IssueToken(now)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	sessionID, ok := manager.ConsumeToken(now.Add(time.Minute), token)
	if !ok || sessionID == "" {
		t.Fatal("expected token to be consumed into valid session")
	}

	if _, ok := manager.ConsumeToken(now.Add(time.Minute), token); ok {
		t.Fatal("expected one-time token to be rejected on second consume")
	}

	expiresAt, ok := manager.Session(now.Add(23*time.Hour), sessionID)
	if !ok {
		t.Fatal("expected active session")
	}
	if expiresAt.Before(now) {
		t.Fatalf("unexpected session expiry: %s", expiresAt)
	}

	if _, ok := manager.Session(now.Add(25*time.Hour), sessionID); ok {
		t.Fatal("expected expired session")
	}
}
