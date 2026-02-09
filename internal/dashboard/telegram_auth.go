package dashboard

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type miniAppVerifier struct {
	botToken string
	maxAge   time.Duration
}

type miniAppUser struct {
	ID int64 `json:"id"`
}

func newMiniAppVerifier(botToken string, maxAge time.Duration) *miniAppVerifier {
	token := strings.TrimSpace(botToken)
	if token == "" {
		return nil
	}
	if maxAge <= 0 {
		maxAge = 24 * time.Hour
	}
	return &miniAppVerifier{
		botToken: token,
		maxAge:   maxAge,
	}
}

func (v *miniAppVerifier) Verify(initData string, now time.Time) (miniAppUser, error) {
	var empty miniAppUser
	if v == nil {
		return empty, errors.New("mini app auth is disabled")
	}

	values, err := url.ParseQuery(initData)
	if err != nil {
		return empty, errors.New("invalid init_data")
	}
	hash := strings.TrimSpace(values.Get("hash"))
	if hash == "" {
		return empty, errors.New("init_data hash is missing")
	}

	values.Del("hash")
	dataCheckString := buildDataCheckString(values)
	if dataCheckString == "" {
		return empty, errors.New("init_data payload is empty")
	}

	if err := validateHash(v.botToken, dataCheckString, hash); err != nil {
		return empty, err
	}
	if err := validateAuthDate(values.Get("auth_date"), now, v.maxAge); err != nil {
		return empty, err
	}

	userJSON := strings.TrimSpace(values.Get("user"))
	if userJSON == "" {
		return empty, errors.New("mini app user is missing")
	}
	var user miniAppUser
	if err := json.Unmarshal([]byte(userJSON), &user); err != nil {
		return empty, errors.New("invalid mini app user payload")
	}
	if user.ID == 0 {
		return empty, errors.New("invalid mini app user id")
	}
	return user, nil
}

func buildDataCheckString(values url.Values) string {
	pairs := make([]string, 0, len(values))
	for key, vals := range values {
		if len(vals) == 0 {
			continue
		}
		pairs = append(pairs, key+"="+vals[0])
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "\n")
}

func validateHash(botToken, dataCheckString, hashHex string) error {
	secretDigest := hmac.New(sha256.New, []byte("WebAppData"))
	_, _ = secretDigest.Write([]byte(botToken))
	secret := secretDigest.Sum(nil)

	digest := hmac.New(sha256.New, secret)
	_, _ = digest.Write([]byte(dataCheckString))
	expected := digest.Sum(nil)

	actual, err := hex.DecodeString(hashHex)
	if err != nil {
		return errors.New("init_data hash is invalid")
	}
	if !hmac.Equal(expected, actual) {
		return errors.New("init_data hash mismatch")
	}
	return nil
}

func validateAuthDate(raw string, now time.Time, maxAge time.Duration) error {
	unixSec, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return errors.New("auth_date is invalid")
	}
	authAt := time.Unix(unixSec, 0).UTC()
	if authAt.After(now.Add(2 * time.Minute)) {
		return errors.New("auth_date is in the future")
	}
	if now.Sub(authAt) > maxAge {
		return errors.New("init_data is expired")
	}
	return nil
}
