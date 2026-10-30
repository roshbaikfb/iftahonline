// Package paymob is Paymob's API as two modules use it: payments (the
// stores' own card payments, ADR-0029) and billing (the platform charging
// merchants' cards on file, ADR-0052). It holds what both need and no
// more: the region's API, one JSON request with Paymob's error shapes,
// and the HMAC-SHA512 over a callback's ordered fields. Each module keeps
// its own adapter on top.
//
// It follows Paymob's own API collections (PaymobAccept/API-Postman-
// Collections).
package paymob

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/retry"
	"github.com/roshbaikfb/iftahonline/internal/platform/text"
)

// Base URLs per region.
const (
	EgyptURL = "https://accept.paymob.com"
	SaudiURL = "https://ksa.paymob.com"
	UAEURL   = "https://uae.paymob.com"
)

const maxResponse = 1 << 20

// Errors. Do wraps them with retry.Permanent: retrying cannot fix them.
var (
	// ErrAuth: Paymob refused the keys (401, 403).
	ErrAuth = errors.New("paymob: the keys were refused")
	// ErrRejected: Paymob refused the request (another 4xx), or the
	// currency has no region.
	ErrRejected = errors.New("paymob: request rejected")
)

// Options configures New.
type Options struct {
	// BaseURL overrides the region chosen by currency (tests).
	BaseURL string
	Timeout time.Duration // per request; default 10 s
	Client  *http.Client
}

// Client sends requests to Paymob.
type Client struct {
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
	return &Client{base: strings.TrimRight(o.BaseURL, "/"), http: o.Client}
}

// BaseFor is the API of currency's region (EGP, SAR, AED).
func (c *Client) BaseFor(currency string) (string, error) {
	if c.base != "" {
		return c.base, nil
	}
	switch currency {
	case "EGP":
		return EgyptURL, nil
	case "SAR":
		return SaudiURL, nil
	case "AED":
		return UAEURL, nil
	}
	return "", retry.Permanent(fmt.Errorf("%w: Paymob does not take %s", ErrRejected, currency))
}

// Do sends one JSON request and decodes the answer into out; auth is the
// whole Authorization value. A 404 is no error (the caller decides);
// 429 and 5xx are retryable; other 4xx are ErrAuth or ErrRejected.
func (c *Client) Do(ctx context.Context, method, rawURL, auth string, in, out any) (int, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, retry.Permanent(err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return 0, retry.Permanent(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("paymob: %s: %w", req.URL.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return resp.StatusCode, fmt.Errorf("paymob: read: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return resp.StatusCode, retry.Permanent(ErrAuth)
	case resp.StatusCode == http.StatusNotFound:
		return resp.StatusCode, nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return resp.StatusCode, fmt.Errorf("paymob: %s: status %d", req.URL.Path, resp.StatusCode)
	case resp.StatusCode >= 400:
		return resp.StatusCode, retry.Permanent(fmt.Errorf("%w: %s (status %d)", ErrRejected, text.Redact(text.Clip(string(raw), 200)), resp.StatusCode))
	}
	if out != nil {
		if err = json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, retry.Permanent(fmt.Errorf("paymob: decode %s: %w", req.URL.Path, err))
		}
	}
	return resp.StatusCode, nil
}

// SignedString concatenates the values of fields in obj, in that order:
// strings as they are, numbers as sent, booleans as true/false, missing
// and null as nothing. A field "a.b" is b inside the object a.
func SignedString(obj map[string]json.RawMessage, fields []string) string {
	var b strings.Builder
	for _, f := range fields {
		raw := lookup(obj, f)
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			b.WriteString(s)
			continue
		}
		b.Write(bytes.TrimSpace(raw)) // numbers and booleans as written
	}
	return b.String()
}

func lookup(obj map[string]json.RawMessage, path string) json.RawMessage {
	parent, child, nested := strings.Cut(path, ".")
	if !nested {
		return obj[parent]
	}
	var inner map[string]json.RawMessage
	if err := json.Unmarshal(obj[parent], &inner); err != nil {
		return nil
	}
	return inner[child]
}

// Callback is a callback's body: its type (TRANSACTION, TOKEN) and its
// object, as sent.
type Callback struct {
	Type string                     `json:"type"`
	Obj  map[string]json.RawMessage `json:"obj"`
}

// ParseCallback decodes a callback body, keeping numbers as written (the
// signature is over them).
func ParseCallback(body []byte) (Callback, error) {
	var cb Callback
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	if err := d.Decode(&cb); err != nil || cb.Obj == nil {
		return Callback{}, errors.New("paymob: not a callback")
	}
	return cb, nil
}

// MAC is the HMAC-SHA512 over obj's fields with secret.
func MAC(secret string, obj map[string]json.RawMessage, fields []string) []byte {
	m := hmac.New(sha512.New, []byte(secret))
	m.Write([]byte(SignedString(obj, fields)))
	return m.Sum(nil)
}

// Verify reports whether hexMAC (the callback's hmac query parameter) is
// obj's signature over fields with secret, in constant time.
func Verify(secret, hexMAC string, obj map[string]json.RawMessage, fields []string) bool {
	got, err := hex.DecodeString(strings.ToLower(hexMAC))
	if err != nil || len(got) != sha512.Size || secret == "" {
		return false
	}
	return hmac.Equal(got, MAC(secret, obj, fields))
}

// Sign is the hmac a callback body carries for fields (stand-ins and
// tests).
func Sign(secret string, body []byte, fields []string) (string, error) {
	cb, err := ParseCallback(body)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(MAC(secret, cb.Obj, fields)), nil
}

// TokenFields are a card token callback's signed fields, in Paymob's
// order (HMAC for card tokens).
func TokenFields() []string {
	return []string{"card_subtype", "created_at", "email", "id", "masked_pan", "merchant_id", "order_id", "token"}
}

// TransactionFields are a transaction callback's signed fields, in
// Paymob's order. One of the source_data fields is the card's masked
// last four digits, as Paymob sends them: it is only read to check the
// signature.
func TransactionFields() []string {
	return []string{
		"amount_cents", "created_at", "currency", "error_occured", "has_parent_transaction", "id", "integration_id",
		"is_3d_secure", "is_auth", "is_capture", "is_refunded", "is_standalone_payment", "is_voided", "order.id", "owner",
		"pending", "source_data.pan", "source_data.sub_type", "source_data.type", "success", // nocarddata:allow masked last four digits, part of Paymob's signed field list
	}
}
