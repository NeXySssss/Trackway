package dashboard

import (
	"testing"
	"time"
)

func TestRateLimiterAllow(t *testing.T) {
	t.Parallel()

	limiter := newRateLimiter(2, time.Minute)
	now := time.Now().UTC()
	key := "127.0.0.1"

	if !limiter.Allow(now, key) {
		t.Fatal("first request should be allowed")
	}
	if !limiter.Allow(now.Add(10*time.Second), key) {
		t.Fatal("second request should be allowed")
	}
	if limiter.Allow(now.Add(20*time.Second), key) {
		t.Fatal("third request in same window should be rejected")
	}
	if !limiter.Allow(now.Add(2*time.Minute), key) {
		t.Fatal("request after window should be allowed")
	}
}
