package dashboard

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMiniAppVerifierVerify(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0).UTC()
	initData := buildSignedInitData("test-bot-token", now, 511741383)
	verifier := newMiniAppVerifier("test-bot-token", 24*time.Hour)

	user, err := verifier.Verify(initData, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("verify init data: %v", err)
	}
	if user.ID != 511741383 {
		t.Fatalf("unexpected user id: %d", user.ID)
	}
}

func TestMiniAppVerifierRejectsTamperedHash(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0).UTC()
	initData := buildSignedInitData("test-bot-token", now, 1)
	initData = strings.Replace(initData, "hash=", "hash=00", 1)

	verifier := newMiniAppVerifier("test-bot-token", 24*time.Hour)
	if _, err := verifier.Verify(initData, now); err == nil {
		t.Fatal("expected hash mismatch error")
	}
}

func buildSignedInitData(botToken string, authAt time.Time, userID int64) string {
	userPayload, _ := json.Marshal(map[string]any{
		"id": userID,
	})
	values := url.Values{}
	values.Set("auth_date", strconv.FormatInt(authAt.Unix(), 10))
	values.Set("query_id", "AAH4f2MFAAAAANh_ZxP8fA")
	values.Set("user", string(userPayload))

	pairs := make([]string, 0, len(values))
	for key, vals := range values {
		pairs = append(pairs, key+"="+vals[0])
	}
	sort.Strings(pairs)
	dataCheck := strings.Join(pairs, "\n")

	secretDigest := hmac.New(sha256.New, []byte("WebAppData"))
	_, _ = secretDigest.Write([]byte(botToken))
	secret := secretDigest.Sum(nil)

	digest := hmac.New(sha256.New, secret)
	_, _ = digest.Write([]byte(dataCheck))
	hash := hex.EncodeToString(digest.Sum(nil))

	values.Set("hash", hash)
	return values.Encode()
}
