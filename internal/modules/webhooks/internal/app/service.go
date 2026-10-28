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
	ErrBadChallenge  = errs.BadRequest("the handshake carries no usable challenge", nil)
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

// Challenger is a Provider that proves ownership of a connection's URL
// with a GET handshake before using it (Meta's hub.challenge).
type Challenger interface {
	// Challenge checks the handshake's query against the store's
	// connection (the store is in ctx) and returns the challenge to
	// echo. It returns domain.ErrForged for a wrong token and
	// domain.ErrUnknownConnection.
	Challenge(ctx context.Context, connectionID ulid.ID, query url.Values) (string, error)
}

// PlatformProvider is the side of a provider whose account the platform
// owns and shares between stores (one WhatsApp number, ADR-0038): its
// deliveries arrive at one URL without a store.
type PlatformProvider interface {
	// Name is the provider's path segment, e.g. "whatsapp".
	Name() string
	// VerifyPlatform checks a delivery with the platform's secret and
	// splits it into the events of each store. It drops events it cannot
	// place (logging them), returns domain.ErrForged, or another error
	// when it could not decide (the provider will retry).
	VerifyPlatform(ctx context.Context, req Request) ([]domain.Part, error)
	// ChallengePlatform answers the URL handshake against the platform's
	// token (domain.ErrForged when it does not match).
	ChallengePlatform(ctx context.Context, query url.Values) (string, error)
	// AcceptTx enqueues the processing job of a part's receipt, in the
	// transaction of the part's store that stores it.
	AcceptTx(ctx context.Context, tx pgx.Tx, r domain.Receipt) error
}

// acceptor enqueues a receipt's processing job.
type acceptor interface {
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
	// PerPlatform bounds one platform-owned account's deliveries, which
	// carry every store's events.
	PerPlatform ratelimit.Limit
}

// DefaultLimits leave room for a provider's bursts (a carrier sends one
// delivery per shipment update, and retries backlogs after an outage).
func DefaultLimits() Limits {
	return Limits{
		PerIP:         ratelimit.Limit{Burst: 1200, Period: time.Minute},
		PerConnection: ratelimit.Limit{Burst: 600, Period: time.Minute},
		PerPlatform:   ratelimit.Limit{Burst: 6000, Period: time.Minute},
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
	platform  map[string]PlatformProvider
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
	return &Service{db: d.DB, ids: d.IDs, limiter: d.Limiter, limits: d.Limits, log: d.Log, providers: map[string]Provider{},
		platform: map[string]PlatformProvider{}}
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

// RegisterPlatform adds a platform-owned account's provider. Names are
// unique among them; a provider may also be registered with Register for
// its merchants' own accounts.
func (s *Service) RegisterPlatform(p PlatformProvider) error {
	name := p.Name()
	if !domain.ValidProvider(name) {
		return fmt.Errorf("webhooks: invalid provider name %q", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.platform[name]; dup {
		return fmt.Errorf("webhooks: platform provider %q registered twice", name)
	}
	s.platform[name] = p
	return nil
}

func (s *Service) platformProvider(name string) (PlatformProvider, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.platform[name]
	return p, ok
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

	return s.accept(ctx, p, provider, conn, v.EventID, req.Header.Get("Content-Type"), req.Body)
}

// accept stores one event of the store in ctx once, with its processing
// job, and reports whether it was a replay.
func (s *Service) accept(ctx context.Context, p acceptor, provider string, conn ulid.ID, eventID, contentType string, body []byte) (duplicate bool, err error) {
	id := s.ids.New()
	r := domain.Receipt{ID: id, Provider: provider, ConnectionID: conn, EventID: eventID, ReceivedAt: domain.ReceivedAtOf(id)}
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		// Expired receipts go first, so an event id older than the
		// retention counts as new.
		if txErr := store.Purge(ctx, tx, r.ReceivedAt.Add(-domain.ReceiptRetention)); txErr != nil {
			return txErr
		}
		accepted, txErr := store.Accept(ctx, tx, r, contentType, body)
		if txErr != nil || !accepted {
			duplicate = !accepted
			return txErr
		}
		return p.AcceptTx(ctx, tx, r)
	})
	if err != nil {
		return false, err
	}
	storeID, _ := tenant.FromContext(ctx) //nolint:errcheck // the transaction above needed it
	s.log.InfoContext(ctx, "webhook received", "provider", provider, "store_id", storeID.String(),
		"connection_id", conn.String(), "receipt_id", id.String(), "duplicate", duplicate)
	return duplicate, nil
}

// Challenge answers a provider's URL handshake for a connection of the
// store in ctx. Providers without one are ErrNotFound.
func (s *Service) Challenge(ctx context.Context, provider string, conn ulid.ID, query url.Values, ip netip.Addr) (string, error) {
	storeID, err := tenant.FromContext(ctx)
	if err != nil {
		return "", err
	}
	p, ok := s.provider(provider)
	if !ok {
		return "", ErrNotFound
	}
	c, ok := p.(Challenger)
	if !ok {
		return "", ErrNotFound
	}
	if err = s.allow(ctx, "ip:"+ip.String(), s.limits.PerIP); err != nil {
		return "", err
	}
	challenge, err := c.Challenge(ctx, conn, query)
	switch {
	case errors.Is(err, domain.ErrForged):
		s.reject(ctx, "handshake", provider, storeID, conn, Request{}, ip)
		return "", ErrForged
	case errors.Is(err, domain.ErrUnknownConnection):
		s.reject(ctx, "unknown_connection", provider, storeID, conn, Request{}, ip)
		return "", ErrNotFound
	case err != nil:
		return "", fmt.Errorf("webhooks: challenge %s: %w", provider, err)
	}
	if !domain.ValidChallenge(challenge) {
		return "", ErrBadChallenge
	}
	return challenge, nil
}

// ChallengePlatform answers a provider's URL handshake for its
// platform-owned account.
func (s *Service) ChallengePlatform(ctx context.Context, provider string, query url.Values, ip netip.Addr) (string, error) {
	p, ok := s.platformProvider(provider)
	if !ok {
		return "", ErrNotFound
	}
	if err := s.allow(ctx, "ip:"+ip.String(), s.limits.PerIP); err != nil {
		return "", err
	}
	challenge, err := p.ChallengePlatform(ctx, query)
	switch {
	case errors.Is(err, domain.ErrForged):
		s.reject(ctx, "handshake", provider, tenant.StoreID{}, ulid.ID{}, Request{}, ip)
		return "", ErrForged
	case err != nil:
		return "", fmt.Errorf("webhooks: challenge %s: %w", provider, err)
	}
	if !domain.ValidChallenge(challenge) {
		return "", ErrBadChallenge
	}
	return challenge, nil
}

// ReceivePlatform handles one delivery to a platform-owned account: it
// is verified as a whole, then each store's event is accepted once in
// that store, in its own transaction. It reports a replay when every
// event it placed was a replay. A failure part-way returns the error, so the provider retries
// the delivery; the events already accepted are then replays.
func (s *Service) ReceivePlatform(ctx context.Context, provider string, req Request, ip netip.Addr) (duplicate bool, err error) {
	p, ok := s.platformProvider(provider)
	if !ok {
		return false, ErrNotFound
	}
	if err = s.allow(ctx, "ip:"+ip.String(), s.limits.PerIP); err != nil {
		return false, err
	}
	if err = s.allow(ctx, "platform:"+provider, s.limits.PerPlatform); err != nil {
		return false, err
	}
	parts, err := p.VerifyPlatform(ctx, req)
	switch {
	case errors.Is(err, domain.ErrForged):
		s.reject(ctx, "signature", provider, tenant.StoreID{}, ulid.ID{}, req, ip)
		return false, ErrForged
	case err != nil:
		return false, fmt.Errorf("webhooks: verify %s: %w", provider, err)
	}
	if len(parts) > domain.MaxParts {
		s.reject(ctx, "too_many_parts", provider, tenant.StoreID{}, ulid.ID{}, req, ip)
		return false, ErrBadEventID
	}
	for _, part := range parts {
		if !part.Valid() {
			s.reject(ctx, "part", provider, part.Store, part.ConnectionID, req, ip)
			return false, ErrBadEventID
		}
	}
	news, dups := 0, 0
	for _, part := range parts {
		dup, err := s.accept(tenant.WithStore(ctx, part.Store), p, provider, part.ConnectionID, part.EventID, "application/json", part.Body)
		if errors.Is(err, domain.ErrUnknownStore) {
			s.log.WarnContext(ctx, "webhook part for no store dropped", "provider", provider, "store_id", part.Store.String())
			continue
		}
		if err != nil {
			return false, err
		}
		if dup {
			dups++
		} else {
			news++
		}
	}
	return dups > 0 && news == 0, nil
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
