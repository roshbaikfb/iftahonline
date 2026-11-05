// Package stripe is Stripe's API as two modules use it: billing (the
// platform's own account charging merchants outside Egypt, ADR-0091)
// and, next, payments (a merchant's own account for its storefront). It
// holds what both need and no more: one form-encoded request with
// Stripe's error shapes, and the webhook signature check. Each module
// keeps its own adapter on top.
//
// It follows Stripe's API reference (docs.stripe.com/api), pinned to one
// API version so that a change of the account's default version changes
// nothing here.
package stripe

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/retry"
	"github.com/roshbaikfb/iftahonline/internal/platform/text"
)

// BaseURL is Stripe's API.
const BaseURL = "https://api.stripe.com"

// APIVersion is the version every request asks for.
const APIVersion = "2024-06-20"

// Tolerance is how old a webhook's signed timestamp may be (Stripe's
// default): older deliveries are replays.
const Tolerance = 5 * time.Minute

const maxResponse = 1 << 20

// Errors. Do wraps ErrAuth and ErrRejected with retry.Permanent:
// retrying cannot fix them.
var (
	// ErrAuth: Stripe refused the key (401, 403).
	ErrAuth = errors.New("stripe: the key was refused")
	// ErrRejected: Stripe refused the request (another 4xx).
	ErrRejected = errors.New("stripe: request rejected")
	// ErrSignature: a webhook not signed with the endpoint's secret, or
	// signed too long ago.
	ErrSignature = errors.New("stripe: webhook not signed")
)

// CardError is a declined card (402): an answer, not a failure. Stripe
// sends the PaymentIntent it declined.
type CardError struct {
	Code          string          `json:"code"`
	DeclineCode   string          `json:"decline_code"`
	Message       string          `json:"message"`
	PaymentIntent json.RawMessage `json:"payment_intent"`
}

func (e *CardError) Error() string {
	return "stripe: card declined: " + text.Clip(e.Code+" "+e.DeclineCode, 80)
}

// Reason is what the merchant is told: Stripe's message, or its codes.
func (e *CardError) Reason() string {
	for _, s := range []string{e.Message, e.DeclineCode, e.Code} {
		if s = strings.TrimSpace(s); s != "" {
			return text.Clip(s, 200)
		}
	}
	return "declined"
}

// Options configures New.
type Options struct {
	SecretKey string
	BaseURL   string        // overrides BaseURL (tests)
	Timeout   time.Duration // per request; default 10 s
	Client    *http.Client
}

// Client sends requests to Stripe with one secret key.
type Client struct {
	key  string
	base string
	http *http.Client
}

// New returns a client.
func New(o Options) *Client {
	if o.Timeout <= 0 {
		o.Timeout = 10 * time.Second
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: o.Timeout}
	}
	base := strings.TrimRight(o.BaseURL, "/")
	if base == "" {
		base = BaseURL
	}
	return &Client{key: o.SecretKey, base: base, http: o.Client}
}

// Do sends one request and decodes the answer into out. form is the
// body of a POST and the query of a GET; idempotencyKey makes a POST
// safe to repeat (Stripe keeps a key's answer for 24 hours).
//
// A 404 is no error (the caller decides); 402 is a *CardError; 409
// (a key's request still running), 429 and 5xx are retryable; other 4xx
// are ErrAuth or ErrRejected.
func (c *Client) Do(ctx context.Context, method, path string, form url.Values, idempotencyKey string, out any) (int, error) {
	if c.key == "" {
		return 0, retry.Permanent(fmt.Errorf("%w: no secret key", ErrAuth))
	}
	u := c.base + path
	var body io.Reader
	if method == http.MethodGet {
		if len(form) > 0 {
			u += "?" + form.Encode()
		}
	} else {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return 0, retry.Permanent(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Stripe-Version", APIVersion)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("stripe: %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return resp.StatusCode, fmt.Errorf("stripe: read: %w", err)
	}
	switch s := resp.StatusCode; {
	case s == http.StatusUnauthorized || s == http.StatusForbidden:
		return s, retry.Permanent(ErrAuth)
	case s == http.StatusNotFound:
		return s, nil
	case s == http.StatusPaymentRequired:
		var e struct {
			Error CardError `json:"error"`
		}
		if json.Unmarshal(raw, &e) != nil {
			return s, retry.Permanent(fmt.Errorf("%w: an unreadable decline", ErrRejected))
		}
		return s, &e.Error
	case s == http.StatusConflict || s == http.StatusTooManyRequests || s >= 500:
		return s, fmt.Errorf("stripe: %s: status %d", path, s)
	case s >= 400:
		return s, retry.Permanent(fmt.Errorf("%w: %s (status %d)", ErrRejected, text.Redact(text.Clip(errorMessage(raw), 200)), s))
	}
	if out != nil {
		if err = json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, retry.Permanent(fmt.Errorf("stripe: decode %s: %w", path, err))
		}
	}
	return resp.StatusCode, nil
}

// errorMessage is Stripe's message of an error body, else the body.
func errorMessage(raw []byte) string {
	var e struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
		return e.Error.Type + ": " + e.Error.Message
	}
	return string(raw)
}

// Verify checks a webhook's Stripe-Signature header against the
// endpoint's secret: an HMAC-SHA256 of "<t>.<body>" in one of its v1
// values, compared in constant time, and t within Tolerance of now.
func Verify(secret, header string, body []byte, now time.Time) error {
	if secret == "" {
		return ErrSignature
	}
	ts, sigs, ok := ParseSignature(header)
	if !ok {
		return ErrSignature
	}
	if d := now.Sub(time.Unix(ts, 0)); d > Tolerance || d < -Tolerance {
		return ErrSignature
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts, 10)))
	mac.Write([]byte{'.'})
	mac.Write(body)
	want := mac.Sum(nil)
	for _, s := range sigs {
		if hmac.Equal(want, s) {
			return nil
		}
	}
	return ErrSignature
}

// ParseSignature reads a Stripe-Signature header: its timestamp and its
// v1 signatures (several while a secret is being rolled); other schemes
// are ignored. ok is false without a timestamp or a v1 signature.
func ParseSignature(header string) (ts int64, v1 [][]byte, ok bool) {
	if len(header) > 4096 {
		return 0, nil, false
	}
	seenT := false
	for _, item := range strings.Split(header, ",") {
		k, v, found := strings.Cut(strings.TrimSpace(item), "=")
		if !found {
			continue
		}
		switch k {
		case "t":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n <= 0 || seenT {
				return 0, nil, false
			}
			ts, seenT = n, true
		case "v1":
			if b, err := hex.DecodeString(v); err == nil && len(b) == sha256.Size && len(v1) < 8 {
				v1 = append(v1, b)
			}
		}
	}
	return ts, v1, seenT && len(v1) > 0
}

// Sign is the Stripe-Signature header Stripe would send for body at t
// (tests and stand-ins).
func Sign(secret string, body []byte, t time.Time) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(t.Unix(), 10) + "."))
	mac.Write(body)
	return "t=" + strconv.FormatInt(t.Unix(), 10) + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

// Event is a webhook's event: its ID, its type and the object it is
// about.
type Event struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Data struct {
		Object json.RawMessage `json:"object"`
	} `json:"data"`
}

// ParseEvent reads a webhook's body.
func ParseEvent(body []byte) (Event, error) {
	var e Event
	if err := json.Unmarshal(body, &e); err != nil {
		return Event{}, fmt.Errorf("stripe: event: %w", err)
	}
	if !strings.HasPrefix(e.ID, "evt_") || len(e.ID) > 255 || e.Type == "" || len(e.Data.Object) == 0 {
		return Event{}, errors.New("stripe: not an event")
	}
	return e, nil
}

// Currency is Stripe's spelling of an ISO currency (lower case).
func Currency(iso string) string { return strings.ToLower(iso) }
