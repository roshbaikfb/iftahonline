// Package captcha verifies Cloudflare Turnstile tokens (ADR-0022). The
// storefront asks for one only when an order submission looks risky, so
// most shoppers never see a challenge.
package captcha

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/retry"
)

// SiteverifyURL is Turnstile's verification endpoint.
const SiteverifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// ErrUnavailable means the token could not be checked (network or
// Cloudflare failure); the caller decides how to treat the submission.
var ErrUnavailable = errors.New("captcha: verification unavailable")

// Verifier checks a widget's token.
type Verifier interface {
	Verify(ctx context.Context, token, remoteIP string) (bool, error)
}

// Turnstile verifies tokens with Cloudflare.
type Turnstile struct {
	secret string
	url    string
	client *http.Client
	retry  retry.Policy
}

// NewTurnstile returns a verifier with the site's secret key.
func NewTurnstile(secret string) *Turnstile {
	return &Turnstile{secret: secret, url: SiteverifyURL, client: &http.Client{Timeout: 3 * time.Second},
		retry: retry.Policy{MaxAttempts: 3, Initial: 100 * time.Millisecond, Max: time.Second}}
}

// WithURL points the verifier at another endpoint (tests).
func (t *Turnstile) WithURL(u string) *Turnstile {
	c := *t
	c.url = u
	return &c
}

// Verify reports whether token is a fresh, valid answer. A token works
// once, so every attempt of one verification sends the same
// idempotency_key: Cloudflare answers a retry as it answered the first
// try instead of calling the token spent.
func (t *Turnstile) Verify(ctx context.Context, token, remoteIP string) (bool, error) {
	if token == "" || len(token) > 2048 {
		return false, nil
	}
	key, err := idempotencyKey()
	if err != nil {
		return false, err
	}
	form := url.Values{"secret": {t.secret}, "response": {token}, "idempotency_key": {key}}
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	var ok bool
	err = retry.Do(ctx, t.retry, func(ctx context.Context) error {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, t.url, strings.NewReader(form.Encode()))
		if reqErr != nil {
			return retry.Permanent(reqErr)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res, doErr := t.client.Do(req)
		if doErr != nil {
			return doErr
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode >= 500 || res.StatusCode == http.StatusTooManyRequests {
			return fmt.Errorf("captcha: siteverify status %d", res.StatusCode)
		}
		body, readErr := io.ReadAll(io.LimitReader(res.Body, 16<<10))
		if readErr != nil {
			return readErr
		}
		var parseErr error
		ok, parseErr = ParseResponse(body)
		return retry.Permanent(parseErr)
	})
	if err != nil {
		return false, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return ok, nil
}

// ParseResponse reads siteverify's answer: only an explicit success is a
// pass.
func ParseResponse(body []byte) (bool, error) {
	var r struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return false, fmt.Errorf("captcha: bad siteverify answer: %w", err)
	}
	return r.Success, nil
}

func idempotencyKey() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // a version 4 UUID, as Cloudflare expects
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}
