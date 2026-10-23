// Package app holds the inbound webhook use cases (ADR-0024): receive a
// delivery (rate-limit, verify, accept once), and let provider modules
// read what they accepted and record what processing did.
package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/clientinfo"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Errors the HTTP layer renders.
var (
	ErrNotFound      = errs.NotFound("webhook_not_found", "no such webhook endpoint")
	ErrForged        = errs.Unauthenticated("the webhook signature does not verify")
	ErrBadEventID    = errs.BadRequest("the webhook carries no usable event id", nil)
	ErrReceiptAbsent = errs.NotFound("webhook_receipt_not_found", "webhook receipt not found")
)

// Request is a delivery as received: verified before anything is stored.
type Request struct {
	Header http.Header
	Query  url.Values
	Body   []byte
}

// Provider is a carrier's or gateway's side of inbound webhooks. Its
// module implements it and registers it at startup.
type Provider interface {
	// Name is the provider's path segment, e.g. "bosta".
	Name() string
	// Verify checks a delivery against the store's connection (the store
	// is in ctx) and returns the provider's event id. It returns
	// domain.ErrForged or domain.ErrUnknownConnection, or another error
	// when it could not decide (the provider will retry).
	Verify(ctx context.Context, connectionID ulid.ID, req Request) (domain.Verified, error)
	// AcceptTx enqueues the module's processing job for r in the
	// transaction that stores r, so a receipt and its job exist together.
	AcceptTx(ctx context.Context, tx pgx.Tx, r domain.Receipt) error
}

// Limiter is the shared rate limiter.
type Limiter interface {
	Allow(ctx context.Context, key string, lim ratelimit.Limit) (ratelimit.Result, error)
}

// Limits are the request budgets.
type Limits struct {
	PerIP         ratelimit.Limit
	PerConnection ratelimit.Limit
}

// DefaultLimits leave room for a provider's bursts (a carrier sends one
// delivery per shipment update, and retries backlogs after an outage).
func DefaultLimits() Limits {
	return Limits{
		PerIP:         ratelimit.Limit{Burst: 1200, Period: time.Minute},
		PerConnection: ratelimit.Limit{Burst: 600, Period: time.Minute},
	}
}

// Service implements the use cases.
type Service struct {
	db      postgres.Beginner
	ids     *ulid.Generator
	limiter Limiter
	limits  Limits
	log     *slog.Logger

	mu        sync.RWMutex
	providers map[string]Provider
}

// Deps are the service's dependencies.
type Deps struct {
	DB      postgres.Beginner
	IDs     *ulid.Generator
	Limiter Limiter
	Limits  Limits
	Log     *slog.Logger
}

// NewService builds the service.
func NewService(d Deps) *Service {
	return &Service{db: d.DB, ids: d.IDs, limiter: d.Limiter, limits: d.Limits, log: d.Log, providers: map[string]Provider{}}
}

// Register adds a provider. Names are unique.
func (s *Service) Register(p Provider) error {
	name := p.Name()
	if !domain.ValidProvider(name) {
		return fmt.Errorf("webhooks: invalid provider name %q", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.providers[name]; dup {
		return fmt.Errorf("webhooks: provider %q registered twice", name)
	}
	s.providers[name] = p
	return nil
}

func (s *Service) provider(name string) (Provider, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.providers[name]
	return p, ok
}

// Receive handles one delivery for the store in ctx. It reports whether
// the delivery is a replay of an accepted provider event.
//
// Nothing is stored before the signature verifies. A forged delivery is
// ErrForged and a warning in the log, with the body's hash and the
// client's network but neither the body nor its headers.
func (s *Service) Receive(ctx context.Context, provider string, conn ulid.ID, req Request, ip netip.Addr) (duplicate bool, err error) {
	storeID, err := tenant.FromContext(ctx)
	if err != nil {
		return false, err
	}
	p, ok := s.provider(provider)
	if !ok {
		return false, ErrNotFound
	}
	if err = s.allow(ctx, "ip:"+ip.String(), s.limits.PerIP); err != nil {
		return false, err
	}
	if err = s.allow(ctx, "conn:"+provider+":"+storeID.String()+":"+conn.String(), s.limits.PerConnection); err != nil {
		return false, err
	}

	v, err := p.Verify(ctx, conn, req)
	switch {
	case errors.Is(err, domain.ErrForged):
		s.reject(ctx, "signature", provider, storeID, conn, req, ip)
		return false, ErrForged
	case errors.Is(err, domain.ErrUnknownConnection):
		s.reject(ctx, "unknown_connection", provider, storeID, conn, req, ip)
		return false, ErrNotFound
	case err != nil:
		return false, fmt.Errorf("webhooks: verify %s: %w", provider, err)
	}
	if !domain.ValidEventID(v.EventID) {
		s.reject(ctx, "event_id", provider, storeID, conn, req, ip)
		return false, ErrBadEventID
	}

	id := s.ids.New()
	r := domain.Receipt{ID: id, Provider: provider, ConnectionID: conn, EventID: v.EventID, ReceivedAt: domain.ReceivedAtOf(id)}
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		// Expired receipts go first, so an event id older than the
		// retention counts as new.
		if txErr := store.Purge(ctx, tx, r.ReceivedAt.Add(-domain.ReceiptRetention)); txErr != nil {
			return txErr
		}
		accepted, txErr := store.Accept(ctx, tx, r, req.Header.Get("Content-Type"), req.Body)
		if txErr != nil || !accepted {
			duplicate = !accepted
			return txErr
		}
		return p.AcceptTx(ctx, tx, r)
	})
	if err != nil {
		return false, err
	}
	s.log.InfoContext(ctx, "webhook received", "provider", provider, "store_id", storeID.String(),
		"connection_id", conn.String(), "receipt_id", id.String(), "duplicate", duplicate)
	return duplicate, nil
}

func (s *Service) allow(ctx context.Context, key string, lim ratelimit.Limit) error {
	r, err := s.limiter.Allow(ctx, key, lim)
	if err != nil {
		return errs.Unavailable(5*time.Second, err)
	}
	if !r.Allowed {
		return errs.RateLimited(r.RetryAfter)
	}
	return nil
}

// reject logs a refused delivery. The body may be an attacker's: only its
// hash and size are logged.
func (s *Service) reject(ctx context.Context, reason, provider string, storeID tenant.StoreID, conn ulid.ID, req Request, ip netip.Addr) {
	sum := sha256.Sum256(req.Body)
	s.log.WarnContext(ctx, "webhook rejected", "reason", reason, "provider", provider, "store_id", storeID.String(),
		"connection_id", conn.String(), "ip_prefix", clientinfo.IPPrefix(ip), "body_sha256", hex.EncodeToString(sum[:]),
		"body_bytes", len(req.Body))
}

// Get returns an accepted receipt of the store in ctx with its raw body
// (nil once the body expired), for the provider's processing job.
func (s *Service) Get(ctx context.Context, id ulid.ID) (r domain.Receipt, contentType string, body []byte, err error) {
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		r, contentType, body, txErr = store.Get(ctx, tx, id)
		return txErr
	})
	if errors.Is(err, domain.ErrNotFound) {
		err = ErrReceiptAbsent
	}
	return r, contentType, body, err
}

// MarkProcessedTx records what processing did, in the transaction that
// did it. Only the first outcome is kept; it reports whether this call
// recorded it.
func (s *Service) MarkProcessedTx(ctx context.Context, tx pgx.Tx, id ulid.ID, outcome string) (bool, error) {
	if !domain.ValidOutcome(outcome) {
		return false, fmt.Errorf("webhooks: invalid outcome %q", outcome)
	}
	return store.MarkProcessed(ctx, tx, id, outcome)
}
