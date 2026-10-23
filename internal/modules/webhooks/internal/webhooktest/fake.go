// Package webhooktest has a provider for the webhooks module's tests: an
// HMAC-SHA256 signature over the body with a secret per connection, and
// the event id in the body's "id".
package webhooktest

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// SignatureHeader carries the fake provider's signature.
const SignatureHeader = "X-Fake-Signature"

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
