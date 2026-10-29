// Package capi sends conversions to the ad providers' server APIs
// (ADR-0043): Meta's Conversions API, TikTok's Events API, Snapchat's
// Conversions API and GA4's Measurement Protocol. Each event carries the
// event_id the browser's pixel reported, so the provider counts it once.
//
// Built from the providers' documentation; unverified against a live
// account until a sandbox is available. Personal data is normalized and
// hashed (SHA-256) before it leaves; tokens are never logged.
package capi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/money"
)

// Conversion names, as the storefront reports them.
const (
	AddToCart        = "AddToCart"
	InitiateCheckout = "InitiateCheckout"
	Purchase         = "Purchase"
)

// Event is one conversion.
type Event struct {
	Name       string
	ID         string // the event_id the browser reported too
	Time       time.Time
	ValueMinor int64
	Units      int // the currency's decimal digits
	Currency   string
	Items      []string // variant IDs
	OrderID    string   // Purchase only
	URL        string   // the page it happened on
	User       User
}

// User is what a provider matches the event to a person with. Every
// field is optional.
type User struct {
	Phone      string // E.164, "+201012345678"
	ExternalID string // our customer ID
	Country    string // ISO 3166-1 alpha-2
	IP         string
	UserAgent  string
	FBP, FBC   string // Meta's cookies
	TTP        string // TikTok's cookie
	TTCLID     string // TikTok's click ID
	ScCID      string // Snapchat's click ID
}

// Pixel is a store's connection to one provider.
type Pixel struct {
	ID       string
	Token    string
	TestCode string
}

// Sender sends one event to one provider.
type Sender interface {
	Send(ctx context.Context, p Pixel, e Event) error
}

// ErrRejected is a provider refusing the event for good (a bad token or
// pixel, a malformed event): retrying cannot help.
var ErrRejected = errors.New("capi: rejected")

// Timeout bounds one request to a provider.
const Timeout = 10 * time.Second

// NewClient is the client the senders share: bounded by Timeout, and it
// never follows a redirect, since Snap's and GA4's URLs carry the token.
// A redirect answers as its own status (not 2xx, so retried).
func NewClient() *http.Client {
	return &http.Client{Timeout: Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// hash is the hex SHA-256 the providers ask for; "" stays "".
func hash(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// phoneDigits is an E.164 number without "+": Meta's and Snapchat's
// normalized form.
func phoneDigits(e164 string) string { return strings.TrimPrefix(e164, "+") }

// value is the amount in decimal, as JSON: 450.00 for 45000 minor units
// of a two-digit currency.
func value(minor int64, units int) json.Number { return json.Number(money.Decimal(minor, units)) }

// post sends body as JSON and maps the answer: 2xx is sent; 4xx but 408
// and 429 is ErrRejected; anything else may be retried.
func post(ctx context.Context, c *http.Client, endpoint string, header http.Header, body any) ([]byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("capi: encode: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("capi: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header[k] = v
	}
	res, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("capi: send: %w", scrub(err))
	}
	defer func() { _ = res.Body.Close() }()
	out, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if err != nil {
		return nil, fmt.Errorf("capi: read: %w", err)
	}
	switch s := res.StatusCode; {
	case s >= 200 && s < 300:
		return out, nil
	case s >= 400 && s < 500 && s != http.StatusRequestTimeout && s != http.StatusTooManyRequests:
		return nil, fmt.Errorf("%w: status %d", ErrRejected, s)
	default:
		return nil, fmt.Errorf("capi: status %d", s)
	}
}

// scrub drops the URL from a transport error: some providers take the
// token in the query string, and errors are logged.
func scrub(err error) error {
	var u *url.Error
	if errors.As(err, &u) {
		return u.Err
	}
	return err
}
