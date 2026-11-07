package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authtoken"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/mailer"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Platform support on a store's team (C1, the approved mockups). The
// console calls these with the store in ctx, after recording the action
// with its reason in the admin log; each also lands in the store's
// activity log, naming the admin (metadata support_admin).
//
// Removing a member's two-step verification is never done at once: the
// request takes effect SupportDelay later, and the emails sent now carry
// a link that cancels it, so a request made by someone impersonating a
// merchant is stopped by the real one (the owner decided, 2026-11-07).
// Disabling an account and signing it out everywhere are protective, so
// they are immediate.

// SupportDelay is how long a support request waits before it takes
// effect; RecentRequests is how long a closed one stays on the console.
const (
	SupportDelay   = 24 * time.Hour
	RecentRequests = 7 * 24 * time.Hour
)

var (
	errNotMember     = errs.NotFound("member_not_found", "this account is not a member of the store")
	errMFAOff        = errs.Conflict("mfa_not_enabled", "this account has no two-step verification to remove")
	errRequestOpen   = errs.Conflict("request_open", "a request of this kind is already waiting")
	errRequestClosed = errs.NotFound("request_not_found", "this request was cancelled, done, or does not exist")
	errNoReason      = errors.New("identity: support actions need an admin and a reason")
)

// SupportMember is a store member as the console sees them: the phone
// masked (it identifies, it is not for calling).
type SupportMember struct {
	UserID     ulid.ID
	Name       string
	Email      string
	Phone      string // masked, empty when none
	Role       authz.Role
	JoinedAt   time.Time
	MFAEnabled bool
	Disabled   bool
	LastSeenAt time.Time // zero: never
}

// SupportInvite is a pending invitation to the store.
type SupportInvite struct {
	Email     string
	Role      authz.Role
	ExpiresAt time.Time
}

// SupportTeam is a store's team for the console: members, pending
// invitations, and support requests open or closed lately.
type SupportTeam struct {
	Members  []SupportMember
	Invites  []SupportInvite
	Requests []store.SupportRequest
}

// SupportTeam reads the team of the store in ctx in one transaction.
func (s *Service) SupportTeam(ctx context.Context) (SupportTeam, error) {
	var out SupportTeam
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		members, err := store.ListTeam(ctx, tx)
		if err != nil {
			return err
		}
		out.Members = make([]SupportMember, len(members))
		for i, m := range members {
			out.Members[i] = SupportMember{UserID: m.UserID, Name: m.Name, Email: m.Email, Phone: MaskPhone(m.Phone), Role: m.Role,
				JoinedAt: m.JoinedAt, MFAEnabled: m.MFAEnabled, Disabled: m.Disabled, LastSeenAt: m.LastSeenAt}
		}
		invites, err := store.ListInvitations(ctx, tx)
		if err != nil {
			return err
		}
		out.Invites = make([]SupportInvite, len(invites))
		for i, inv := range invites {
			out.Invites[i] = SupportInvite{Email: inv.Email, Role: inv.Role, ExpiresAt: inv.ExpiresAt}
		}
		out.Requests, err = store.ListSupportRequests(ctx, tx, s.now().Add(-RecentRequests))
		return err
	})
	return out, err
}

// MaskPhone keeps an E.164 number's country part and last three digits:
// "+9665••••••412".
func MaskPhone(e164 string) string {
	if len(e164) < 9 {
		return ""
	}
	return e164[:5] + strings.Repeat("•", len(e164)-8) + e164[len(e164)-3:]
}

// SupportLinkEmailArgs sends a support request's cancel link: to the
// member it is about, or to the store's owner (Owner).
type SupportLinkEmailArgs struct {
	jobs.TenantArgs
	TokenID ulid.ID `json:"token_id"`
	Owner   bool    `json:"owner,omitempty"`
}

// Kind implements river.JobArgs.
func (SupportLinkEmailArgs) Kind() string { return "identity.support.link_email" }

// SupportExecuteArgs carries out a support request when its delay ends
// (inserted with ScheduledAt in the request's transaction).
type SupportExecuteArgs struct {
	jobs.TenantArgs
	RequestID ulid.ID `json:"request_id"`
}

// Kind implements river.JobArgs.
func (SupportExecuteArgs) Kind() string { return "identity.support.execute" }

// SupportNoticeArgs tells a user what support did to their account.
type SupportNoticeArgs struct {
	jobs.TenantArgs
	UserID ulid.ID   `json:"user_id"`
	Event  string    `json:"event"`
	At     time.Time `json:"at"`
}

// Kind implements river.JobArgs.
func (SupportNoticeArgs) Kind() string { return "identity.support.notice" }

// SupportCancelledArgs tells the admin who asked that a merchant
// cancelled their request from the email link.
type SupportCancelledArgs struct {
	jobs.TenantArgs
	RequestID ulid.ID `json:"request_id"`
}

// Kind implements river.JobArgs.
func (SupportCancelledArgs) Kind() string { return "identity.support.cancelled" }

// Notice events.
const (
	NoticeMFAReset  = "mfa_reset_done"
	NoticeDisabled  = "disabled"
	NoticeEnabled   = "enabled"
	NoticeSignedOut = "signed_out"
)

func supportLinkKey(id ulid.ID) string { return "identity:support-link:" + id.String() }
func supportLinkAAD(id ulid.ID) string { return "support-link:" + id.String() }

// recordSupport writes the store's activity entry of an admin's action.
func (s *Service) recordSupport(ctx context.Context, tx pgx.Tx, admin ulid.ID, action string, user ulid.ID, meta map[string]any) error {
	return recordSupport(ctx, tx, s.audit, admin, action, user, meta)
}

func recordSupport(ctx context.Context, tx pgx.Tx, rec *Recorder, admin ulid.ID, action string, user ulid.ID, meta map[string]any) error {
	if meta == nil {
		meta = map[string]any{}
	}
	if !admin.IsZero() {
		meta["support_admin"] = admin.String()
	}
	return rec.RecordTx(ctx, tx, audit.Entry{Action: action, TargetType: "user", TargetID: user.String(), Metadata: meta})
}

// member reads userID's account if it is a member of the store in ctx.
func member(ctx context.Context, tx pgx.Tx, userID ulid.ID) (domain.User, authz.Role, error) {
	role, err := store.MemberRole(ctx, tx, userID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.User{}, "", errNotMember
	}
	if err != nil {
		return domain.User{}, "", err
	}
	u, err := store.UserByID(ctx, tx, userID)
	return u, role, err
}

// RequestMFAReset asks for member userID's two-step verification to be
// removed in SupportDelay: the request, its cancel links to the member
// and the owner, and the job that carries it out are written in one
// transaction.
func (s *Service) RequestMFAReset(ctx context.Context, admin, userID ulid.ID, reason string) (store.SupportRequest, error) {
	if admin.IsZero() || reason == "" {
		return store.SupportRequest{}, errNoReason
	}
	targs, err := jobs.ForTenant(ctx)
	if err != nil {
		return store.SupportRequest{}, err
	}
	var req store.SupportRequest
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		u, _, txErr := member(ctx, tx, userID)
		if txErr != nil {
			return txErr
		}
		if !u.MFAEnabled() {
			return errMFAOff
		}
		req, txErr = store.InsertSupportRequest(ctx, tx, store.SupportRequest{ID: s.ids.New(), Kind: store.KindMFAReset, UserID: userID,
			RequestedBy: admin, Reason: reason, EffectiveAt: s.now().Add(SupportDelay)})
		if errors.Is(txErr, store.ErrRequestOpen) {
			return errRequestOpen
		}
		if txErr != nil {
			return txErr
		}
		if txErr = s.sendSupportLinksTx(ctx, tx, targs, req, u); txErr != nil {
			return txErr
		}
		if _, txErr = s.jobs.InsertTx(ctx, tx, SupportExecuteArgs{TenantArgs: targs, RequestID: req.ID},
			&river.InsertOpts{ScheduledAt: req.EffectiveAt}); txErr != nil {
			return fmt.Errorf("identity: schedule support request: %w", txErr)
		}
		return s.recordSupport(ctx, tx, admin, "support.mfa_reset_requested", userID,
			map[string]any{"request_id": req.ID.String(), "reason": reason, "effective_at": req.EffectiveAt.UTC()})
	})
	return req, err
}

// sendSupportLinksTx records a cancel link for the member the request is
// about and one for the store's owner when that is someone else, and
// enqueues their emails, in tx.
func (s *Service) sendSupportLinksTx(ctx context.Context, tx pgx.Tx, targs jobs.TenantArgs, req store.SupportRequest, u domain.User) error {
	type recipient struct {
		user  domain.User
		owner bool
	}
	to := []recipient{{user: u}}
	ownerID, err := store.Owner(ctx, tx)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	if err == nil && ownerID != u.ID {
		owner, ownerErr := store.UserByID(ctx, tx, ownerID)
		if ownerErr != nil {
			return ownerErr
		}
		to = append(to, recipient{user: owner, owner: true})
	}
	ttl := req.EffectiveAt.Sub(s.now()) + time.Hour
	for _, r := range to {
		tok, tokErr := authtoken.New()
		if tokErr != nil {
			return tokErr
		}
		id := s.ids.New()
		if err = store.InsertSupportToken(ctx, tx, id, r.user.ID, req.ID, r.user.Email, tok.Hash(), req.EffectiveAt); err != nil {
			return err
		}
		if err = s.putDelivery(ctx, supportLinkKey(id), supportLinkAAD(id), delivery{Text: string(tok)}, ttl); err != nil {
			return err
		}
		if _, err = s.jobs.InsertTx(ctx, tx, SupportLinkEmailArgs{TenantArgs: targs, TokenID: id, Owner: r.owner}, nil); err != nil {
			return fmt.Errorf("identity: enqueue support link: %w", err)
		}
	}
	return nil
}

// CancelSupportRequest cancels an open request of the store in ctx from
// the console.
func (s *Service) CancelSupportRequest(ctx context.Context, admin, id ulid.ID, reason string) error {
	if admin.IsZero() || reason == "" {
		return errNoReason
	}
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		req, err := store.GetSupportRequest(ctx, tx, id, true)
		if errors.Is(err, domain.ErrNotFound) {
			return errRequestClosed
		}
		if err != nil {
			return err
		}
		ok, err := store.CloseSupportRequest(ctx, tx, id, store.CancelledByConsole)
		if err != nil {
			return err
		}
		if !ok {
			return errRequestClosed
		}
		return s.recordSupport(ctx, tx, admin, "support."+req.Kind+"_cancelled", req.UserID,
			map[string]any{"request_id": id.String(), "reason": reason, "by": store.CancelledByConsole})
	})
}

// SupportLink is what a cancel link's page shows: the request, and whom
// it is about. ForMember is false on the store owner's copy of a link
// about another member, so the page names that member.
type SupportLink struct {
	Kind        string
	Email       string // masked: the address the link was sent to
	MemberName  string
	ForMember   bool
	CreatedAt   time.Time
	EffectiveAt time.Time
}

// openSupportLink reads the request an open cancel link names, with its
// store put in ctx.
func (s *Service) openSupportLink(ctx context.Context, rawToken string) (context.Context, store.SupportLink, error) {
	if err := s.throttle(ctx, "support-link:ip:"+ipKey(ctx), s.limits.LoginPerIP); err != nil {
		return ctx, store.SupportLink{}, err
	}
	tok, ok := authtoken.Parse(rawToken)
	if !ok {
		return ctx, store.SupportLink{}, errLinkGone
	}
	link, err := store.PeekSupportToken(ctx, s.db, tok.Hash())
	if errors.Is(err, domain.ErrNotFound) {
		return ctx, store.SupportLink{}, errLinkGone
	}
	if err != nil {
		return ctx, store.SupportLink{}, err
	}
	return tenant.WithStore(ctx, tenant.StoreID{ID: link.StoreID}), link, nil
}

// CheckSupportLink answers what an open cancel link would cancel,
// without cancelling it (no session: the link may open anywhere).
func (s *Service) CheckSupportLink(ctx context.Context, rawToken string) (SupportLink, error) {
	ctx, link, err := s.openSupportLink(ctx, rawToken)
	if err != nil {
		return SupportLink{}, err
	}
	var (
		req    store.SupportRequest
		member domain.User
	)
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		var txErr error
		if req, txErr = store.GetSupportRequest(ctx, tx, link.RequestID, false); txErr != nil {
			return txErr
		}
		member, txErr = store.UserByID(ctx, tx, req.UserID)
		return txErr
	})
	if errors.Is(err, domain.ErrNotFound) || err == nil && !req.Open() {
		return SupportLink{}, errLinkGone
	}
	if err != nil {
		return SupportLink{}, err
	}
	return SupportLink{Kind: req.Kind, Email: mailer.MaskEmail(link.Email), MemberName: member.Name, ForMember: link.UserID == req.UserID,
		CreatedAt: req.CreatedAt, EffectiveAt: req.EffectiveAt}, nil
}

// CancelSupportLink cancels the request an open link names: its every
// link closes, the store's log records it, and the admin who asked is
// told.
func (s *Service) CancelSupportLink(ctx context.Context, rawToken string) error {
	ctx, link, err := s.openSupportLink(ctx, rawToken)
	if err != nil {
		return err
	}
	targs, err := jobs.ForTenant(ctx)
	if err != nil {
		return err
	}
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		req, txErr := store.GetSupportRequest(ctx, tx, link.RequestID, true)
		if errors.Is(txErr, domain.ErrNotFound) {
			return errLinkGone
		}
		if txErr != nil {
			return txErr
		}
		ok, txErr := store.CloseSupportRequest(ctx, tx, req.ID, store.CancelledByLink)
		if txErr != nil {
			return txErr
		}
		if !ok {
			return errLinkGone
		}
		if _, txErr = s.jobs.InsertTx(ctx, tx, SupportCancelledArgs{TenantArgs: targs, RequestID: req.ID}, nil); txErr != nil {
			return fmt.Errorf("identity: enqueue support cancelled: %w", txErr)
		}
		return s.recordSupport(ctx, tx, ulid.ID{}, "support."+req.Kind+"_cancelled", req.UserID,
			map[string]any{"request_id": req.ID.String(), "by": store.CancelledByLink, "email": mailer.MaskEmail(link.Email)})
	})
}

// ExecuteSupportRequest carries out a request whose delay ended (the
// store in ctx); a request cancelled meanwhile is left.
func (s *Service) ExecuteSupportRequest(ctx context.Context, id ulid.ID) error {
	return supportRunner{db: s.db, jobs: s.jobs, audit: s.audit, now: s.now}.execute(ctx, id)
}

// supportRunner carries out support requests; the worker builds one
// without the rest of the service.
type supportRunner struct {
	db    DB
	jobs  jobs.Inserter
	audit *Recorder
	now   func() time.Time
}

func (s supportRunner) execute(ctx context.Context, id ulid.ID) error {
	targs, err := jobs.ForTenant(ctx)
	if err != nil {
		return err
	}
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		req, txErr := store.GetSupportRequest(ctx, tx, id, true)
		if errors.Is(txErr, domain.ErrNotFound) {
			return nil
		}
		if txErr != nil {
			return txErr
		}
		if !req.Open() {
			return nil
		}
		if s.now().Before(req.EffectiveAt) {
			return fmt.Errorf("identity: support request %s ran early", id)
		}
		switch req.Kind {
		case store.KindMFAReset:
			if txErr = store.DisableTOTP(ctx, tx, req.UserID); txErr != nil {
				return txErr
			}
			if txErr = store.RevokeUserSessions(ctx, tx, req.UserID, ulid.ID{}); txErr != nil {
				return txErr
			}
			if _, txErr = s.jobs.InsertTx(ctx, tx, SupportNoticeArgs{TenantArgs: targs, UserID: req.UserID, Event: NoticeMFAReset,
				At: s.now().UTC()}, nil); txErr != nil {
				return fmt.Errorf("identity: enqueue support notice: %w", txErr)
			}
		default:
			return fmt.Errorf("identity: unknown support request kind %q", req.Kind)
		}
		if _, txErr = store.CloseSupportRequest(ctx, tx, req.ID, ""); txErr != nil {
			return txErr
		}
		return recordSupport(ctx, tx, s.audit, req.RequestedBy, "support."+req.Kind+"_done", req.UserID, map[string]any{"request_id": req.ID.String()})
	})
}

// SetMemberDisabled disables member userID's account (every session ends
// at once) or enables it again; the account is platform-wide, so it is
// disabled in every store it works on. No change: nothing is recorded.
func (s *Service) SetMemberDisabled(ctx context.Context, admin, userID ulid.ID, disabled bool, reason string) error {
	if admin.IsZero() || reason == "" {
		return errNoReason
	}
	targs, err := jobs.ForTenant(ctx)
	if err != nil {
		return err
	}
	status, action, event := domain.UserActive, "member.enabled", NoticeEnabled
	if disabled {
		status, action, event = domain.UserDisabled, "member.disabled", NoticeDisabled
	}
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if _, _, txErr := member(ctx, tx, userID); txErr != nil {
			return txErr
		}
		changed, txErr := store.SetUserStatus(ctx, tx, userID, status)
		if txErr != nil || !changed {
			return txErr
		}
		if disabled {
			if txErr = store.RevokeUserSessions(ctx, tx, userID, ulid.ID{}); txErr != nil {
				return txErr
			}
		}
		if _, txErr = s.jobs.InsertTx(ctx, tx, SupportNoticeArgs{TenantArgs: targs, UserID: userID, Event: event, At: s.now().UTC()}, nil); txErr != nil {
			return fmt.Errorf("identity: enqueue support notice: %w", txErr)
		}
		return s.recordSupport(ctx, tx, admin, action, userID, map[string]any{"reason": reason})
	})
}

// SignOutMember ends every session of member userID (a stolen account,
// without disabling it).
func (s *Service) SignOutMember(ctx context.Context, admin, userID ulid.ID, reason string) error {
	if admin.IsZero() || reason == "" {
		return errNoReason
	}
	targs, err := jobs.ForTenant(ctx)
	if err != nil {
		return err
	}
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if _, _, txErr := member(ctx, tx, userID); txErr != nil {
			return txErr
		}
		if txErr := store.RevokeUserSessions(ctx, tx, userID, ulid.ID{}); txErr != nil {
			return txErr
		}
		if _, txErr := s.jobs.InsertTx(ctx, tx, SupportNoticeArgs{TenantArgs: targs, UserID: userID, Event: NoticeSignedOut, At: s.now().UTC()},
			nil); txErr != nil {
			return fmt.Errorf("identity: enqueue support notice: %w", txErr)
		}
		return s.recordSupport(ctx, tx, admin, "member.signed_out", userID, map[string]any{"reason": reason})
	})
}
