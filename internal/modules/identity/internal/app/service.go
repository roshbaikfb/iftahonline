package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authtoken"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	mailaddr "github.com/roshbaikfb/iftahonline/internal/platform/email"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/password"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/secretbox"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Limits are the throttles on public auth endpoints (ADR-0005 §4, §6).
type Limits struct {
	SignupPerIP      ratelimit.Limit // sign-up attempts per IP
	LoginPerIP       ratelimit.Limit // login attempts per IP
	LoginFailPerUser ratelimit.Limit // failed logins per email, then locked until refill
	OTPPerPhone      ratelimit.Limit // codes sent to one number (resend spacing)
	OTPPerPhoneDay   ratelimit.Limit // codes sent to one number per day (SMS pumping)
	OTPPerIP         ratelimit.Limit // codes requested from one IP
	MFAFailPerUser   ratelimit.Limit // wrong second factors per user
}

// DefaultLimits are conservative production defaults.
func DefaultLimits() Limits {
	return Limits{
		SignupPerIP:      ratelimit.Limit{Burst: 5, Period: time.Hour},
		LoginPerIP:       ratelimit.Limit{Burst: 30, Period: 15 * time.Minute},
		LoginFailPerUser: ratelimit.Limit{Burst: 5, Period: 15 * time.Minute},
		OTPPerPhone:      ratelimit.Limit{Burst: 1, Period: time.Minute},
		OTPPerPhoneDay:   ratelimit.Limit{Burst: 10, Period: 24 * time.Hour},
		OTPPerIP:         ratelimit.Limit{Burst: 10, Period: time.Hour},
		MFAFailPerUser:   ratelimit.Limit{Burst: 10, Period: 15 * time.Minute},
	}
}

// DB is a pool: it begins transactions and runs platform-wide queries
// (users, sessions) directly. *pgxpool.Pool implements it.
type DB interface {
	postgres.Beginner
	store.Tx
}

// Deps are the service's dependencies.
type Deps struct {
	DB      DB
	Hasher  *password.Hasher
	Limiter *ratelimit.Limiter
	Limits  Limits
	Stores  tenancy.Service
	IDs     *ulid.Generator
	Log     *slog.Logger
	Redis   *redis.Client      // pending challenges and code deliveries
	Keyring *secretbox.Keyring // seals TOTP seeds and pending codes
	Pepper  []byte             // OTP MAC key
	Jobs    jobs.Inserter      // code delivery, invitation mail, device alerts
	// OTPEnabled is false while no SMS/WhatsApp provider is configured in
	// production: phone OTP then answers 503.
	OTPEnabled bool
}

// Service implements the identity use cases.
type Service struct {
	db         DB
	hasher     *password.Hasher
	limiter    *ratelimit.Limiter
	limits     Limits
	stores     tenancy.Service
	ids        *ulid.Generator
	audit      *Recorder
	log        *slog.Logger
	rdb        *redis.Client
	keyring    *secretbox.Keyring
	pepper     []byte
	jobs       jobs.Inserter
	otpEnabled bool
	now        func() time.Time
}

// NewService wires the service.
func NewService(d Deps) *Service {
	return &Service{db: d.DB, hasher: d.Hasher, limiter: d.Limiter, limits: d.Limits, stores: d.Stores, ids: d.IDs,
		audit: NewRecorder(d.IDs), log: d.Log, rdb: d.Redis, keyring: d.Keyring, pepper: d.Pepper, jobs: d.Jobs,
		otpEnabled: d.OTPEnabled, now: time.Now}
}

var (
	errInvalidCredentials = errs.Unauthenticated("invalid email or password")
	errNoSession          = errs.Unauthenticated("authentication required")
	errStoreNotFound      = errs.NotFound("store_not_found", "store not found")
)

// SignupInput creates an account and its first store.
type SignupInput struct {
	Email    string
	Password string
	Name     string
	Store    tenancy.NewStore
}

// AuthResult is a fresh session for a user, or (MFA set) the challenge
// the user must complete with a second factor first.
type AuthResult struct {
	User    domain.User
	StoreID tenant.StoreID // set by sign-up
	Session domain.Session
	Token   authtoken.Token
	MFA     *Challenge
}

// Signup creates the user, the store with its default subdomain, the owner
// membership, a session and the first audit entry in one transaction
// (ADR-0007).
func (s *Service) Signup(ctx context.Context, in SignupInput) (AuthResult, error) {
	email, name, err := validateSignup(in)
	if err != nil {
		return AuthResult{}, err
	}
	if err = s.throttle(ctx, "signup:ip:"+ipKey(ctx), s.limits.SignupPerIP); err != nil {
		return AuthResult{}, err
	}
	hash, err := s.hasher.Hash(ctx, in.Password)
	if err != nil {
		return AuthResult{}, err
	}
	token, err := authtoken.New()
	if err != nil {
		return AuthResult{}, err
	}

	userID := s.ids.New()
	storeID := tenant.StoreID{ID: s.ids.New()}
	ctx = tenant.WithStore(ctx, storeID)
	ctx = authz.WithPrincipal(ctx, authz.Principal{UserID: userID, StoreID: storeID, Role: authz.RoleOwner})

	var res AuthResult
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		u, txErr := store.InsertUser(ctx, tx, userID, email, name, hash)
		if errors.Is(txErr, store.ErrEmailTaken) {
			return errs.Validation(errs.FieldError{Field: "email", Code: "taken", Message: "an account with this email already exists"})
		}
		if txErr != nil {
			return txErr
		}
		st, txErr := s.stores.CreateStoreTx(ctx, tx, in.Store)
		if txErr != nil {
			return prefixFields(txErr, "store.")
		}
		if txErr = store.InsertMembership(ctx, tx, userID, authz.RoleOwner); txErr != nil {
			return txErr
		}
		sess, txErr := s.insertSession(ctx, tx, userID, token)
		if txErr != nil {
			return txErr
		}
		if txErr = s.audit.RecordTx(ctx, tx, audit.Entry{Action: "store.created", TargetType: "store", TargetID: st.ID.String(),
			Metadata: map[string]any{"slug": st.Slug}}); txErr != nil {
			return txErr
		}
		res = AuthResult{User: u, StoreID: st.ID, Session: sess, Token: token}
		return nil
	})
	return res, err
}

func validateSignup(in SignupInput) (email, name string, err error) {
	var fields []errs.FieldError
	email, ok := mailaddr.Normalize(in.Email)
	if !ok {
		fields = append(fields, errs.FieldError{Field: "email", Code: "invalid", Message: "enter a valid email address"})
	}
	name, fe := domain.ValidateName(in.Name)
	if fe != nil {
		fields = append(fields, *fe)
	}
	if fe := domain.ValidatePassword(in.Password, in.Email); fe != nil {
		fields = append(fields, *fe)
	}
	if len(fields) > 0 {
		return "", "", errs.Validation(fields...)
	}
	return email, name, nil
}

// prefixFields namespaces another module's validation fields, e.g. the
// store's "name" becomes "store.name" next to the user's "name".
func prefixFields(err error, prefix string) error {
	e, ok := errors.AsType[*errs.Error](err)
	if !ok || e.Kind != errs.KindValidation {
		return err
	}
	fields := make([]errs.FieldError, len(e.Fields))
	for i, f := range e.Fields {
		f.Field = prefix + f.Field
		fields[i] = f
	}
	return errs.Validation(fields...)
}

// Login checks an email and password and opens a session.
//
// Unknown emails, wrong passwords and disabled accounts all get the same
// answer after the same work. Failures are counted per email; once the
// budget is spent, attempts are refused (429) until it refills, even with
// the right password, so guessing cannot continue.
func (s *Service) Login(ctx context.Context, rawEmail, pw string) (AuthResult, error) {
	if err := s.throttle(ctx, "login:ip:"+ipKey(ctx), s.limits.LoginPerIP); err != nil {
		return AuthResult{}, err
	}
	email, ok := mailaddr.Normalize(rawEmail)
	if !ok || len(pw) > 4*domain.MaxPasswordLen {
		return AuthResult{}, errInvalidCredentials
	}
	failKey := "login:fail:" + email
	if err := s.peek(ctx, failKey, s.limits.LoginFailPerUser); err != nil {
		return AuthResult{}, err
	}

	u, err := store.UserByEmail(ctx, s.db, email)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		if err = s.hasher.VerifyDummy(ctx, pw); err != nil {
			return AuthResult{}, err
		}
		return AuthResult{}, s.fail(ctx, failKey)
	case err != nil:
		return AuthResult{}, err
	}
	match, rehash, err := s.hasher.Verify(ctx, pw, u.PasswordHash)
	if err != nil {
		return AuthResult{}, err
	}
	if !match || u.Status != domain.UserActive {
		return AuthResult{}, s.fail(ctx, failKey)
	}
	if err = s.limiter.Reset(ctx, failKey); err != nil {
		s.log.WarnContext(ctx, "identity: reset login failures", "err", err)
	}
	if rehash {
		if h, hashErr := s.hasher.Hash(ctx, pw); hashErr == nil {
			if setErr := store.SetPasswordHash(ctx, s.db, u.ID, h); setErr != nil {
				s.log.WarnContext(ctx, "identity: password rehash", "err", setErr)
			}
		}
	}
	if u.MFAEnabled() {
		return s.startMFA(ctx, u)
	}
	return s.openSession(ctx, u)
}

func (s *Service) fail(ctx context.Context, key string) error {
	if _, err := s.limiter.Allow(ctx, key, s.limits.LoginFailPerUser); err != nil {
		return errs.Unavailable(5*time.Second, err)
	}
	return errInvalidCredentials
}

// insertSession opens a session in tx and records the device; a device
// the user has not used before (when they have others) enqueues a
// new-device alert in the same transaction.
func (s *Service) insertSession(ctx context.Context, tx pgx.Tx, userID ulid.ID, token authtoken.Token) (domain.Session, error) {
	m := metaFrom(ctx)
	now := s.now()
	ua, ipPrefix := domain.TruncateUserAgent(m.UserAgent), domain.IPPrefix(m.IP)
	sess, err := store.InsertSession(ctx, tx, store.NewSession{
		ID: s.ids.New(), UserID: userID, Token: token, UserAgent: ua, IPPrefix: ipPrefix,
		ExpiresAt: domain.ExpiresAt(now, now),
	})
	if err != nil || m.DeviceID == "" {
		return sess, err
	}
	isNew, hadOthers, err := store.TouchDevice(ctx, tx, userID, domain.DeviceHash(m.DeviceID), ua, ipPrefix)
	if err != nil {
		return domain.Session{}, err
	}
	if isNew && hadOthers {
		args := NewDeviceAlertArgs{UserID: userID, UserAgent: ua, IPPrefix: ipPrefix, At: now.UTC()}
		if _, err := s.jobs.InsertTx(ctx, tx, args, nil); err != nil {
			return domain.Session{}, fmt.Errorf("identity: enqueue device alert: %w", err)
		}
	}
	return sess, nil
}

// throttle consumes one event; Redis failures fail closed (503).
func (s *Service) throttle(ctx context.Context, key string, lim ratelimit.Limit) error {
	r, err := s.limiter.Allow(ctx, key, lim)
	if err != nil {
		return errs.Unavailable(5*time.Second, err)
	}
	if !r.Allowed {
		return errs.RateLimited(r.RetryAfter)
	}
	return nil
}

func (s *Service) peek(ctx context.Context, key string, lim ratelimit.Limit) error {
	r, err := s.limiter.Peek(ctx, key, lim)
	if err != nil {
		return errs.Unavailable(5*time.Second, err)
	}
	if !r.Allowed {
		return errs.RateLimited(r.RetryAfter)
	}
	return nil
}

func ipKey(ctx context.Context) string {
	if ip := metaFrom(ctx).IP; ip.IsValid() {
		return ip.String()
	}
	return "unknown"
}

// Authenticate resolves a token on routes outside a store.
func (s *Service) Authenticate(ctx context.Context, raw string) (Identity, error) {
	token, ok := authtoken.Parse(raw)
	if !ok {
		return Identity{}, errNoSession
	}
	as, err := store.SessionByToken(ctx, s.db, token)
	if errors.Is(err, domain.ErrNotFound) || err == nil && as.UserStatus != domain.UserActive {
		return Identity{}, errNoSession
	}
	if err != nil {
		return Identity{}, err
	}
	s.touch(ctx, s.db, as)
	return Identity{UserID: as.UserID, SessionID: as.ID}, nil
}

// AuthenticateInStore resolves a token and the user's membership in the
// store in ctx with one query. No session: 401. Not a member: 404, the
// same as a store that does not exist (ADR-0006).
func (s *Service) AuthenticateInStore(ctx context.Context, raw string) (authz.Principal, error) {
	token, ok := authtoken.Parse(raw)
	if !ok {
		return authz.Principal{}, errNoSession
	}
	storeID, err := tenant.FromContext(ctx)
	if err != nil {
		return authz.Principal{}, errStoreNotFound
	}
	var p authz.Principal
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		as, txErr := store.SessionByTokenInStore(ctx, tx, token)
		if errors.Is(txErr, domain.ErrNotFound) || txErr == nil && as.UserStatus != domain.UserActive {
			return errNoSession
		}
		if txErr != nil {
			return txErr
		}
		if as.Role == "" {
			return errStoreNotFound
		}
		s.touch(ctx, tx, as)
		p = authz.Principal{UserID: as.UserID, SessionID: as.ID, StoreID: storeID, Role: as.Role}
		return nil
	})
	return p, err
}

// touch slides the session's expiry at most once per SessionTouchEvery.
func (s *Service) touch(ctx context.Context, db store.Tx, as store.ActiveSession) {
	now := s.now()
	if now.Sub(as.LastSeen) < domain.SessionTouchEvery {
		return
	}
	if err := store.TouchSession(ctx, db, as.ID, domain.ExpiresAt(as.CreatedAt, now)); err != nil {
		s.log.WarnContext(ctx, "identity: touch session", "err", err)
	}
}

// Logout revokes the current session.
func (s *Service) Logout(ctx context.Context) error {
	id, ok := IdentityFrom(ctx)
	if !ok {
		return errNoSession
	}
	_, err := store.RevokeSession(ctx, s.db, id.UserID, id.SessionID)
	return err
}

// Membership is the signed-in user's role in one store.
type Membership = store.Membership

// Me returns the signed-in user and their memberships across stores.
func (s *Service) Me(ctx context.Context) (domain.User, []Membership, error) {
	id, ok := IdentityFrom(ctx)
	if !ok {
		return domain.User{}, nil, errNoSession
	}
	var (
		u  domain.User
		ms []Membership
	)
	err := postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		if u, err = store.UserByID(ctx, tx, id.UserID); err != nil {
			return err
		}
		ms, err = store.MyMemberships(ctx, tx, id.UserID)
		return err
	})
	if errors.Is(err, domain.ErrNotFound) {
		return domain.User{}, nil, errNoSession
	}
	return u, ms, err
}

// Sessions lists the signed-in user's live sessions.
func (s *Service) Sessions(ctx context.Context) ([]domain.Session, ulid.ID, error) {
	id, ok := IdentityFrom(ctx)
	if !ok {
		return nil, ulid.ID{}, errNoSession
	}
	ss, err := store.ListSessions(ctx, s.db, id.UserID)
	return ss, id.SessionID, err
}

// RevokeSession ends one of the signed-in user's sessions. Another user's
// session ID is not found.
func (s *Service) RevokeSession(ctx context.Context, sessionID ulid.ID) error {
	id, ok := IdentityFrom(ctx)
	if !ok {
		return errNoSession
	}
	found, err := store.RevokeSession(ctx, s.db, id.UserID, sessionID)
	if err != nil {
		return err
	}
	if !found {
		return errs.NotFound("session_not_found", "session not found")
	}
	return nil
}
