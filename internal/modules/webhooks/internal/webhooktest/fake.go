// Package webhooktest has a provider for the webhooks module's tests: an
// HMAC-SHA256 signature over the body with a secret per connection, and
// the event id in the body's "id". Its URL handshake echoes
// hub.challenge when hub.verify_token is the connection's secret.
//
// Its platform-owned account signs with PlatformToken; a delivery is
// {"events":[{"store":…,"connection":…,"id":…}]}, one part per event.
package webhooktest

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"sync"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// SignatureHeader carries the fake provider's signature.
const SignatureHeader = "X-Fake-Signature"

// PlatformToken signs the platform-owned account's deliveries and is
// its handshake token.
const PlatformToken = "fake-platform-secret"

// Provider is the fake provider.
type Provider struct {
	mu       sync.Mutex
	secrets  map[string][]byte
	accepted []domain.Receipt
	// FailAccept makes AcceptTx fail (the receipt must roll back).
	FailAccept error
}

// New returns a provider with no connections.
func New() *Provider { return &Provider{secrets: map[string][]byte{}} }

// Name implements app.Provider.
func (*Provider) Name() string { return "fake" }

// Connect gives store a connection signed with secret.
func (p *Provider) Connect(store tenant.StoreID, conn ulid.ID, secret []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.secrets[store.String()+"/"+conn.String()] = secret
}

// Sign returns the signature header value for body.
func Sign(secret, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// Verify implements app.Provider.
func (p *Provider) Verify(ctx context.Context, conn ulid.ID, req app.Request) (domain.Verified, error) {
	store, err := tenant.FromContext(ctx)
	if err != nil {
		return domain.Verified{}, err
	}
	p.mu.Lock()
	secret, ok := p.secrets[store.String()+"/"+conn.String()]
	p.mu.Unlock()
	if !ok {
		return domain.Verified{}, domain.ErrUnknownConnection
	}
	got, err := hex.DecodeString(req.Header.Get(SignatureHeader))
	if err != nil {
		return domain.Verified{}, domain.ErrForged
	}
	m := hmac.New(sha256.New, secret)
	m.Write(req.Body)
	if !hmac.Equal(got, m.Sum(nil)) {
		return domain.Verified{}, domain.ErrForged
	}
	var body struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(req.Body, &body) //nolint:errcheck // a body without an id fails the event id check
	return domain.Verified{EventID: body.ID}, nil
}

// AcceptTx implements app.Provider.
func (p *Provider) AcceptTx(_ context.Context, _ pgx.Tx, r domain.Receipt) error {
	if p.FailAccept != nil {
		return p.FailAccept
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.accepted = append(p.accepted, r)
	return nil
}

// Accepted returns the receipts AcceptTx saw.
func (p *Provider) Accepted() []domain.Receipt {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]domain.Receipt(nil), p.accepted...)
}

// Challenge implements app.Challenger.
func (p *Provider) Challenge(ctx context.Context, conn ulid.ID, query url.Values) (string, error) {
	store, err := tenant.FromContext(ctx)
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	secret, ok := p.secrets[store.String()+"/"+conn.String()]
	p.mu.Unlock()
	if !ok {
		return "", domain.ErrUnknownConnection
	}
	return challenge(secret, query)
}

func challenge(secret []byte, query url.Values) (string, error) {
	if query.Get("hub.mode") != "subscribe" || !hmac.Equal([]byte(query.Get("hub.verify_token")), secret) {
		return "", domain.ErrForged
	}
	return query.Get("hub.challenge"), nil
}

// Platform is the fake provider's platform-owned account. It shares the
// accepted receipts of the connections' Provider.
type Platform struct{ *Provider }

// Name implements app.PlatformProvider.
func (Platform) Name() string { return "fakeplatform" }

// Event is one store's event in a platform delivery.
type Event struct {
	Store      string `json:"store"`
	Connection string `json:"connection"`
	ID         string `json:"id"`
}

// VerifyPlatform implements app.PlatformProvider: events whose store or
// connection does not parse are dropped.
func (Platform) VerifyPlatform(_ context.Context, req app.Request) ([]domain.Part, error) {
	got, err := hex.DecodeString(req.Header.Get(SignatureHeader))
	if err != nil || !hmac.Equal(got, mac([]byte(PlatformToken), req.Body)) {
		return nil, domain.ErrForged
	}
	var body struct {
		Events []Event `json:"events"`
	}
	if err = json.Unmarshal(req.Body, &body); err != nil {
		return nil, nil
	}
	parts := make([]domain.Part, 0, len(body.Events))
	for _, e := range body.Events {
		store, err := tenant.ParseStoreID(e.Store)
		if err != nil {
			continue
		}
		conn, err := ulid.Parse(e.Connection)
		if err != nil {
			continue
		}
		b, err := json.Marshal(e)
		if err != nil {
			return nil, err
		}
		parts = append(parts, domain.Part{Store: store, ConnectionID: conn, EventID: e.ID, Body: b})
	}
	return parts, nil
}

// ChallengePlatform implements app.PlatformProvider.
func (Platform) ChallengePlatform(_ context.Context, query url.Values) (string, error) {
	return challenge([]byte(PlatformToken), query)
}

func mac(secret, body []byte) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return m.Sum(nil)
}
