// Package app holds the super-admin use cases: creating admins (CLI),
// signing in with password + mandatory TOTP, browsing the store
// directory, and suspending or reactivating stores.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/authtoken"
	"github.com/roshbaikfb/iftahonline/internal/platform/clientinfo"
	"github.com/roshbaikfb/iftahonline/internal/platform/cursor"
	"github.com/roshbaikfb/iftahonline/internal/platform/email"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/password"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/secretbox"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/totp"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// TOTPIssuer labels the console's entry in authenticator apps.
const TOTPIssuer = "iftahonline admin"

// Limits throttle console sign-in.
type Limits struct {
	LoginPerIP      ratelimit.Limit
	LoginFailPerKey ratelimit.Limit // failures per email
}

// DefaultLimits are deliberately tight: there are few admins.
func DefaultLimits() Limits {
	return Limits{
		LoginPerIP:      ratelimit.Limit{Burst: 10, Period: 15 * time.Minute},
		LoginFailPerKey: ratelimit.Limit{Burst: 5, Period: 30 * time.Minute},
	}
}

// Deps are the service's dependencies.
type Deps struct {
	DB        store.DB // the app_admin pool
	Hasher    *password.Hasher
	Limiter   *ratelimit.Limiter // nil for the CLI, which does not log in
	Limits    Limits
	Keyring   *secretbox.Keyring
	IDs       *ulid.Generator
	Log       *slog.Logger
	Directory tenancy.Directory // nil for the CLI
	Stores    tenancy.Service   // nil for the CLI
	// Impersonate opens support sessions; nil refuses them (the CLI).
	Impersonate Impersonator
	// DashboardURL is where the admin exchanges an impersonation code.
	DashboardURL string
	// Plans is billing's plan catalog on the console's role; Enforced
	// tells keys the code checks. Nil refuses the catalog (the CLI).
	Plans    billing.Catalog
	Enforced func(key string) bool
	// Overrides are entitlements' per-store feature flags (ADR-0057);
	// nil refuses them (the CLI).
	Overrides entitlements.Overrides
	// Billing is billing for the console and Redis is pinged by the
	// health page (ADR-0058); nil for the CLI.
	Billing billing.Console
	Redis   *redis.Client
	// Agencies are billing's agency accounts (ADR-0059); nil for the CLI.
	Agencies billing.Agencies
}

// Service implements the console.
type Service struct {
	d   Deps
	now func() time.Time
}

// NewService wires the service.
func NewService(d Deps) *Service { return &Service{d: d, now: time.Now} }

var (
	errInvalidLogin = errs.Unauthenticated("invalid email, password or code")
	errNoSession    = errs.Unauthenticated("admin authentication required")
	errStoreState   = errs.Conflict("store_state", "the store is not in a state that allows this")
)

func totpAAD(id ulid.ID) []byte { return []byte("admin-totp:" + id.String()) }

// Created is a new admin and the TOTP enrolment shown once.
type Created struct {
	Admin  domain.Admin
	Secret string // base32
	URI    string // otpauth://
}

// CreateAdmin adds an admin with a fresh TOTP seed (CLI only: there is no
// HTTP endpoint that creates admins).
func (s *Service) CreateAdmin(ctx context.Context, rawEmail, rawName, pw string) (Created, error) {
	addr, ok := email.Normalize(rawEmail)
	if !ok {
		return Created{}, errs.Validation(errs.FieldError{Field: "email", Code: "invalid", Message: "invalid email"})
	}
	name, err := domain.ValidateName(rawName)
	if err != nil {
		return Created{}, err
	}
	if err = domain.ValidatePassword(pw); err != nil {
		return Created{}, err
	}
	hash, err := s.d.Hasher.Hash(ctx, pw)
	if err != nil {
		return Created{}, err
	}
	seed, err := totp.NewSecret()
	if err != nil {
		return Created{}, err
	}
	id := s.d.IDs.New()
	sealed, err := s.d.Keyring.Seal(seed, totpAAD(id))
	if err != nil {
		return Created{}, err
	}
	a, err := store.InsertAdmin(ctx, s.d.DB, id, addr, name, hash, sealed)
	if errors.Is(err, store.ErrEmailTaken) {
		return Created{}, errs.Conflict("admin_exists", "an admin with this email already exists")
	}
	if err != nil {
		return Created{}, err
	}
	if err := s.audit(ctx, nil, "admin.created", "admin", a.ID.String(), nil); err != nil {
		return Created{}, err
	}
	return Created{Admin: a, Secret: totp.EncodeSecret(seed), URI: totp.URI(seed, TOTPIssuer, a.Email)}, nil
}

// RequestMeta is the caller's device, from the HTTP layer.
type RequestMeta struct {
	UserAgent string
	IP        netip.Addr
}

type metaKey struct{}

// WithRequestMeta returns ctx carrying m.
func WithRequestMeta(ctx context.Context, m RequestMeta) context.Context {
	return context.WithValue(ctx, metaKey{}, m)
}

func metaFrom(ctx context.Context) RequestMeta {
	if m, ok := ctx.Value(metaKey{}).(RequestMeta); ok {
		return m
	}
	return RequestMeta{}
}

// Identity is the signed-in admin.
type Identity struct {
	AdminID   ulid.ID
	SessionID ulid.ID
}

type identityKey struct{}

// WithIdentity returns ctx carrying id.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// IdentityFrom returns the signed-in admin, if any.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok && !id.AdminID.IsZero()
}

// LoginResult is a new admin session.
type LoginResult struct {
	Admin   domain.Admin
	Token   authtoken.Token
	Expires time.Time
}

// Login needs email, password AND a current TOTP code in one step: there
// is no password-only state to attack. Failures are throttled per IP and
// per email, and all look the same.
func (s *Service) Login(ctx context.Context, rawEmail, pw, code string) (LoginResult, error) {
	ip := "unknown"
	if a := metaFrom(ctx).IP; a.IsValid() {
		ip = a.String()
	}
	if err := s.allow(ctx, "login:ip:"+ip, s.d.Limits.LoginPerIP, true); err != nil {
		return LoginResult{}, err
	}
	addr, ok := email.Normalize(rawEmail)
	if !ok || len(pw) > 512 {
		return LoginResult{}, errInvalidLogin
	}
	failKey := "login:fail:" + addr
	if err := s.allow(ctx, failKey, s.d.Limits.LoginFailPerKey, false); err != nil {
		return LoginResult{}, err
	}
	a, err := store.AdminByEmail(ctx, s.d.DB, addr)
	if errors.Is(err, domain.ErrNotFound) {
		if err = s.d.Hasher.VerifyDummy(ctx, pw); err != nil {
			return LoginResult{}, err
		}
		return LoginResult{}, s.fail(ctx, failKey)
	}
	if err != nil {
		return LoginResult{}, err
	}
	match, _, err := s.d.Hasher.Verify(ctx, pw, a.PasswordHash)
	if err != nil {
		return LoginResult{}, err
	}
	if !match || !a.Active || !s.totpOK(ctx, a, code) {
		return LoginResult{}, s.fail(ctx, failKey)
	}
	if err = s.d.Limiter.Reset(ctx, failKey); err != nil {
		s.d.Log.WarnContext(ctx, "admin: reset failures", "err", err)
	}

	token, err := authtoken.New()
	if err != nil {
		return LoginResult{}, err
	}
	m, now := metaFrom(ctx), s.now()
	sess, err := store.InsertSession(ctx, s.d.DB, s.d.IDs.New(), a.ID, token,
		clientinfo.TruncateUserAgent(m.UserAgent), clientinfo.IPPrefix(m.IP), domain.ExpiresAt(now, now))
	if err != nil {
		return LoginResult{}, err
	}
	if err := store.RecordLogin(ctx, s.d.DB, a.ID); err != nil {
		return LoginResult{}, err
	}
	if err := s.audit(ctx, &a.ID, "admin.signed_in", "session", sess.ID.String(), nil); err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Admin: a, Token: token, Expires: sess.ExpiresAt}, nil
}

func (s *Service) totpOK(ctx context.Context, a domain.Admin, code string) bool {
	seed, err := s.d.Keyring.Open(a.TOTPSecret, totpAAD(a.ID))
	if err != nil {
		s.d.Log.ErrorContext(ctx, "admin: cannot open totp seed", "admin_id", a.ID.String(), "err", err)
		return false
	}
	step, ok := totp.Verify(seed, code, s.now(), a.TOTPLastStep)
	if !ok {
		return false
	}
	claimed, err := store.UseTOTPStep(ctx, s.d.DB, a.ID, step)
	return err == nil && claimed
}

// allow consumes (or, with peek=false, only checks) a rate-limit slot.
func (s *Service) allow(ctx context.Context, key string, lim ratelimit.Limit, consume bool) error {
	n := 0
	if consume {
		n = 1
	}
	r, err := s.d.Limiter.AllowN(ctx, key, lim, n)
	if err != nil {
		return errs.Unavailable(5*time.Second, err)
	}
	if !r.Allowed {
		return errs.RateLimited(r.RetryAfter)
	}
	return nil
}

func (s *Service) fail(ctx context.Context, key string) error {
	if _, err := s.d.Limiter.Allow(ctx, key, s.d.Limits.LoginFailPerKey); err != nil {
		return errs.Unavailable(5*time.Second, err)
	}
	return errInvalidLogin
}

// Authenticate resolves a session token.
func (s *Service) Authenticate(ctx context.Context, raw string) (Identity, error) {
	t, ok := authtoken.Parse(raw)
	if !ok {
		return Identity{}, errNoSession
	}
	sess, err := store.SessionByToken(ctx, s.d.DB, t)
	if errors.Is(err, domain.ErrNotFound) || err == nil && !sess.AdminActive {
		return Identity{}, errNoSession
	}
	if err != nil {
		return Identity{}, err
	}
	if now := s.now(); now.Sub(sess.LastSeen) >= domain.SessionTouchEvery {
		if err := store.TouchSession(ctx, s.d.DB, sess.ID, domain.ExpiresAt(sess.CreatedAt, now)); err != nil {
			s.d.Log.WarnContext(ctx, "admin: touch session", "err", err)
		}
	}
	return Identity{AdminID: sess.AdminID, SessionID: sess.ID}, nil
}

// Logout ends the current session.
func (s *Service) Logout(ctx context.Context) error {
	id, ok := IdentityFrom(ctx)
	if !ok {
		return errNoSession
	}
	if err := store.RevokeSession(ctx, s.d.DB, id.SessionID); err != nil {
		return err
	}
	return s.audit(ctx, &id.AdminID, "admin.signed_out", "session", id.SessionID.String(), nil)
}

// Me returns the signed-in admin.
func (s *Service) Me(ctx context.Context) (domain.Admin, error) {
	id, ok := IdentityFrom(ctx)
	if !ok {
		return domain.Admin{}, errNoSession
	}
	return store.AdminByID(ctx, s.d.DB, id.AdminID)
}

// Stores searches the directory.
func (s *Service) Stores(ctx context.Context, query, status, cursor string, limit int) (tenancy.DirectoryPage, error) {
	if _, ok := IdentityFrom(ctx); !ok {
		return tenancy.DirectoryPage{}, errNoSession
	}
	return s.d.Directory.Search(ctx, query, status, cursor, limit)
}

// Store returns one store and its domains.
func (s *Service) Store(ctx context.Context, id tenant.StoreID) (tenancy.Store, []tenancy.Domain, error) {
	if _, ok := IdentityFrom(ctx); !ok {
		return tenancy.Store{}, nil, errNoSession
	}
	return s.d.Directory.Store(ctx, id)
}

// Suspend takes a store offline (storefront 503) and records why.
func (s *Service) Suspend(ctx context.Context, id tenant.StoreID, reason string) (tenancy.Store, error) {
	return s.setStatus(ctx, id, reason, tenancy.StatusActive, tenancy.StatusSuspended, "store.suspended")
}

// Reactivate brings a suspended store back.
func (s *Service) Reactivate(ctx context.Context, id tenant.StoreID, reason string) (tenancy.Store, error) {
	return s.setStatus(ctx, id, reason, tenancy.StatusSuspended, tenancy.StatusActive, "store.reactivated")
}

// setStatus records the action in the admin log first, then changes the
// store through the tenancy module inside the store's own tenant
// transaction (RLS, cache invalidation and the store's audit log all
// apply). The two writes are in different roles' databases, so the admin
// entry is written first: a failed change is then visible as an attempt,
// never an unrecorded change.
func (s *Service) setStatus(ctx context.Context, id tenant.StoreID, rawReason string, from, to tenancy.StoreStatus, action string) (tenancy.Store, error) {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return tenancy.Store{}, errNoSession
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return tenancy.Store{}, err
	}
	cur, _, err := s.d.Directory.Store(ctx, id)
	if err != nil {
		return tenancy.Store{}, err
	}
	if cur.Status != from {
		return tenancy.Store{}, errStoreState
	}
	if err := s.audit(ctx, &who.AdminID, action, "store", id.String(), map[string]any{"reason": reason, "slug": cur.Slug}); err != nil {
		return tenancy.Store{}, err
	}
	return s.d.Stores.SetStatus(tenant.WithStore(ctx, id), to)
}

// Impersonation is a support session's one-time code.
type Impersonation struct {
	Code          string
	CodeExpiresAt time.Time
	ExpiresAt     time.Time
}

// Impersonator opens a support session as the owner of the store in ctx.
type Impersonator func(ctx context.Context, admin ulid.ID, reason string, d time.Duration, write bool) (Impersonation, error)

// Impersonation bounds (ADR-0055).
const (
	DefaultImpersonation = 30 * time.Minute
	MaxImpersonation     = 60 * time.Minute
	MinImpersonation     = 5 * time.Minute
)

var errBadMinutes = errs.Validation(errs.FieldError{Field: "minutes", Code: "out_of_range", Message: "minutes must be between 5 and 60"})

// ImpersonationURL is the dashboard page that exchanges a code; the code
// travels in the fragment, which browsers never send to a server.
func (s *Service) ImpersonationURL(code string) string {
	return strings.TrimRight(s.d.DashboardURL, "/") + "/impersonate#code=" + code
}

// Impersonate opens a time-limited support session as the owner of store
// id: read-only unless write, for d (0 = 30 minutes; at most 60). Like
// setStatus it records the attempt with the reason in the admin log
// first, so no session exists that the log does not show.
func (s *Service) Impersonate(ctx context.Context, id tenant.StoreID, rawReason string, d time.Duration, write bool) (Impersonation, error) {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return Impersonation{}, errNoSession
	}
	if s.d.Impersonate == nil {
		return Impersonation{}, errs.Unavailable(0, errors.New("admin: impersonation is not wired"))
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return Impersonation{}, err
	}
	if d == 0 {
		d = DefaultImpersonation
	}
	if d < MinImpersonation || d > MaxImpersonation {
		return Impersonation{}, errBadMinutes
	}
	cur, _, err := s.d.Directory.Store(ctx, id)
	if err != nil {
		return Impersonation{}, err
	}
	if err := s.audit(ctx, &who.AdminID, "store.impersonated", "store", id.String(),
		map[string]any{"reason": reason, "slug": cur.Slug, "minutes": int(d / time.Minute), "write": write}); err != nil {
		return Impersonation{}, err
	}
	return s.d.Impersonate(tenant.WithStore(ctx, id), who.AdminID, reason, d, write)
}

// AuditPage is a page of the admin log.
type AuditPage struct {
	Items      []store.AuditEntry
	NextCursor string
}

// Audit lists admin actions, newest first.
func (s *Service) Audit(ctx context.Context, after string, limit int) (AuditPage, error) {
	if _, ok := IdentityFrom(ctx); !ok {
		return AuditPage{}, errNoSession
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		return AuditPage{}, errs.Validation(errs.FieldError{Field: "limit", Code: "too_large", Message: "limit must be at most 200"})
	}
	var at *time.Time
	var bid *ulid.ID
	if after != "" {
		t, id, err := cursor.Decode(after)
		if err != nil {
			return AuditPage{}, errs.Validation(errs.FieldError{Field: "cursor", Code: "invalid", Message: "invalid cursor"})
		}
		at, bid = &t, &id
	}
	items, err := store.ListAudit(ctx, s.d.DB, at, bid, limit+1)
	if err != nil {
		return AuditPage{}, err
	}
	page := AuditPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		page.NextCursor = cursor.Encode(last.OccurredAt, last.ID)
	}
	return page, nil
}

func (s *Service) audit(ctx context.Context, adminID *ulid.ID, action, targetType, targetID string, meta map[string]any) error {
	var raw []byte
	if len(meta) > 0 {
		var err error
		if raw, err = json.Marshal(meta); err != nil {
			return fmt.Errorf("admin: audit metadata: %w", err)
		}
	}
	return store.InsertAudit(ctx, s.d.DB, store.AuditEntry{ID: s.d.IDs.New(), AdminID: adminID, Action: action,
		TargetType: targetType, TargetID: targetID, IPPrefix: clientinfo.IPPrefix(metaFrom(ctx).IP), Metadata: raw})
}
