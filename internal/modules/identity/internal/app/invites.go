package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authtoken"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// InvitationTTL is how long an invitation link works.
const InvitationTTL = 7 * 24 * time.Hour

// inviteDeliveryTTL bounds how long the link waits sealed for the mailer.
const inviteDeliveryTTL = 24 * time.Hour

var (
	errInviteNotFound    = errs.NotFound("invitation_not_found", "invitation not found or expired")
	errInviteExists      = errs.Conflict("invitation_exists", "this email already has an open invitation")
	errAlreadyMember     = errs.Conflict("already_member", "this person is already a member of the store")
	errInviteEmail       = errs.Forbidden("this invitation was sent to a different email address")
	errSignInToAccept    = errs.Unauthenticated("sign in with the invited email to accept")
	errMemberNotFound    = errs.NotFound("member_not_found", "member not found")
	errInvalidInviteRole = errs.Validation(errs.FieldError{Field: "role", Code: "invalid", Message: "role must be manager, support or shipping"})
)

// invitableRole: ownership is never granted by invitation.
func invitableRole(r string) (authz.Role, error) {
	role, err := authz.ParseRole(r)
	if err != nil || role == authz.RoleOwner {
		return "", errInvalidInviteRole
	}
	return role, nil
}

// Invitation tokens are "<store ULID>.<secret>": the store tells accept
// which tenant to open, so no cross-tenant lookup is ever needed; only
// SHA-256(token) is stored.
func newInviteToken(store tenant.StoreID) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("identity: invite token: %w", err)
	}
	return store.String() + "." + base64.RawURLEncoding.EncodeToString(b), nil
}

func parseInviteToken(tok string) (tenant.StoreID, []byte, bool) {
	sid, secret, ok := strings.Cut(strings.TrimSpace(tok), ".")
	if !ok || len(secret) != 43 {
		return tenant.StoreID{}, nil, false
	}
	if _, err := base64.RawURLEncoding.DecodeString(secret); err != nil {
		return tenant.StoreID{}, nil, false
	}
	store, err := tenant.ParseStoreID(sid)
	if err != nil {
		return tenant.StoreID{}, nil, false
	}
	h := sha256.Sum256([]byte(strings.TrimSpace(tok)))
	return store, h[:], true
}

// Invite creates an invitation in the store in ctx and enqueues its email.
// The caller has authorized members.manage.
func (s *Service) Invite(ctx context.Context, rawEmail, rawRole string) (store.Invitation, error) {
	p, ok := authz.PrincipalFrom(ctx)
	if !ok {
		return store.Invitation{}, errNoSession
	}
	email, ok := domain.NormalizeEmail(rawEmail)
	if !ok {
		return store.Invitation{}, errs.Validation(errs.FieldError{Field: "email", Code: "invalid", Message: "enter a valid email address"})
	}
	role, err := invitableRole(rawRole)
	if err != nil {
		return store.Invitation{}, err
	}
	token, err := newInviteToken(p.StoreID)
	if err != nil {
		return store.Invitation{}, err
	}
	_, hash, _ := parseInviteToken(token)
	var inv store.Invitation
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		var txErr error
		inv, txErr = store.InsertInvitation(ctx, tx, store.NewInvitation{
			ID: s.ids.New(), Email: email, Role: role, TokenHash: hash, InvitedBy: p.UserID,
			ExpiresAt: s.now().Add(InvitationTTL),
		})
		switch {
		case errors.Is(txErr, store.ErrAlreadyMember):
			return errAlreadyMember
		case errors.Is(txErr, store.ErrInvitationExists):
			return errInviteExists
		case txErr != nil:
			return txErr
		}
		if txErr = s.putDelivery(ctx, inviteDeliveryKey(inv.ID), "invite:"+inv.ID.String(), delivery{Text: token}, inviteDeliveryTTL); txErr != nil {
			return txErr
		}
		if txErr = s.audit.RecordTx(ctx, tx, audit.Entry{Action: "member.invited", TargetType: "invitation", TargetID: inv.ID.String(),
			Metadata: map[string]any{"role": string(role)}}); txErr != nil {
			return txErr
		}
		t, txErr := jobs.ForTenant(ctx)
		if txErr != nil {
			return txErr
		}
		_, txErr = s.jobs.InsertTx(ctx, tx, InviteEmailArgs{TenantArgs: t, InvitationID: inv.ID}, nil)
		return txErr
	})
	return inv, err
}

func inviteDeliveryKey(id ulid.ID) string { return "identity:invite-delivery:" + id.String() }

// Invitations lists the store's open invitations.
func (s *Service) Invitations(ctx context.Context) ([]store.Invitation, error) {
	var out []store.Invitation
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		out, err = store.ListInvitations(ctx, tx)
		return err
	})
	return out, err
}

// RevokeInvitation cancels an open invitation of the store in ctx.
func (s *Service) RevokeInvitation(ctx context.Context, id ulid.ID) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		ok, err := store.RevokeInvitation(ctx, tx, id)
		if err != nil {
			return err
		}
		if !ok {
			return errInviteNotFound
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "invitation.revoked", TargetType: "invitation", TargetID: id.String()})
	})
}

// AcceptInput accepts an invitation. Name and Password create an account
// when the invited email has none; an existing account must be signed in.
type AcceptInput struct {
	Token    string
	Name     string
	Password string
}

// AcceptResult is the joined store and, for a new account, its session.
type AcceptResult struct {
	StoreID tenant.StoreID
	Role    authz.Role
	User    domain.User
	Auth    *AuthResult // set when a new account was created
}

// AcceptInvitation joins the invited store.
func (s *Service) AcceptInvitation(ctx context.Context, in AcceptInput) (AcceptResult, error) {
	storeID, hash, ok := parseInviteToken(in.Token)
	if !ok {
		return AcceptResult{}, errInviteNotFound
	}
	ctx = tenant.WithStore(ctx, storeID)
	signedIn, hasSession := IdentityFrom(ctx)

	// Look the invitation up first so we know whether an account exists
	// before hashing a password.
	var inv store.Invitation
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		inv, err = store.OpenInvitationByToken(ctx, tx, hash)
		return err
	})
	if errors.Is(err, domain.ErrNotFound) {
		return AcceptResult{}, errInviteNotFound
	}
	if err != nil {
		return AcceptResult{}, err
	}

	var (
		user       domain.User
		newAccount bool
		pwHash     string
	)
	existing, lookupErr := store.UserByEmail(ctx, s.db, inv.Email)
	switch {
	case hasSession:
		if lookupErr != nil || existing.ID != signedIn.UserID {
			return AcceptResult{}, errInviteEmail
		}
		user = existing
	case lookupErr == nil:
		return AcceptResult{}, errSignInToAccept
	case errors.Is(lookupErr, domain.ErrNotFound):
		name, fe := domain.ValidateName(in.Name)
		var fields []errs.FieldError
		if fe != nil {
			fields = append(fields, *fe)
		}
		if fe := domain.ValidatePassword(in.Password, inv.Email); fe != nil {
			fields = append(fields, *fe)
		}
		if len(fields) > 0 {
			return AcceptResult{}, errs.Validation(fields...)
		}
		if pwHash, err = s.hasher.Hash(ctx, in.Password); err != nil {
			return AcceptResult{}, err
		}
		user, newAccount = domain.User{ID: s.ids.New(), Email: inv.Email, Name: name}, true
	default:
		return AcceptResult{}, lookupErr
	}

	var res AcceptResult
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		var txErr error
		res, txErr = s.joinTx(ctx, tx, inv, user, newAccount, pwHash)
		return txErr
	})
	return res, err
}

// joinTx creates the account if needed, adds the membership, closes the
// invitation, audits and (for a new account) opens a session.
func (s *Service) joinTx(ctx context.Context, tx pgx.Tx, inv store.Invitation, user domain.User, newAccount bool, pwHash string) (AcceptResult, error) {
	storeID := inv.StoreID
	if newAccount {
		created, err := store.InsertUser(ctx, tx, user.ID, user.Email, user.Name, pwHash)
		if errors.Is(err, store.ErrEmailTaken) {
			return AcceptResult{}, errSignInToAccept // registered concurrently
		}
		if err != nil {
			return AcceptResult{}, err
		}
		user = created
	}
	err := store.AddMember(ctx, tx, user.ID, inv.Role)
	if errors.Is(err, store.ErrAlreadyMember) {
		return AcceptResult{}, errAlreadyMember
	}
	if err != nil {
		return AcceptResult{}, err
	}
	accepted, err := store.MarkInvitationAccepted(ctx, tx, inv.ID)
	if err != nil {
		return AcceptResult{}, err
	}
	if !accepted {
		return AcceptResult{}, errInviteNotFound // revoked or used concurrently
	}
	actx := authz.WithPrincipal(ctx, authz.Principal{UserID: user.ID, StoreID: storeID, Role: inv.Role})
	if err = s.audit.RecordTx(actx, tx, audit.Entry{Action: "member.joined", TargetType: "user", TargetID: user.ID.String(),
		Metadata: map[string]any{"role": string(inv.Role), "invitation_id": inv.ID.String()}}); err != nil {
		return AcceptResult{}, err
	}
	res := AcceptResult{StoreID: storeID, Role: inv.Role, User: user}
	if newAccount {
		token, err := authtoken.New()
		if err != nil {
			return AcceptResult{}, err
		}
		sess, err := s.insertSession(ctx, tx, user.ID, token)
		if err != nil {
			return AcceptResult{}, err
		}
		res.Auth = &AuthResult{User: user, StoreID: storeID, Session: sess, Token: token}
	}
	return res, nil
}

// Members lists the store's team.
func (s *Service) Members(ctx context.Context) ([]store.Member, error) {
	var out []store.Member
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		out, err = store.ListMembers(ctx, tx)
		return err
	})
	return out, err
}

// SetMemberRole changes a member's role. The owner's role cannot be
// changed and nobody becomes owner this way.
func (s *Service) SetMemberRole(ctx context.Context, userID ulid.ID, rawRole string) error {
	role, err := invitableRole(rawRole)
	if err != nil {
		return err
	}
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		old, err := store.MemberRole(ctx, tx, userID)
		if errors.Is(err, domain.ErrNotFound) {
			return errMemberNotFound
		}
		if err != nil {
			return err
		}
		if old == authz.RoleOwner {
			return errs.Conflict("owner_role_fixed", "the owner's role cannot be changed")
		}
		if _, err := store.SetMemberRole(ctx, tx, userID, role); err != nil {
			return err
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "member.role_changed", TargetType: "user", TargetID: userID.String(),
			Metadata: map[string]any{"from": string(old), "to": string(role)}})
	})
}

// RemoveMember takes a member out of the store; their next request there
// gets 404. The owner cannot be removed.
func (s *Service) RemoveMember(ctx context.Context, userID ulid.ID) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		old, err := store.MemberRole(ctx, tx, userID)
		if errors.Is(err, domain.ErrNotFound) {
			return errMemberNotFound
		}
		if err != nil {
			return err
		}
		if old == authz.RoleOwner {
			return errs.Conflict("owner_cannot_be_removed", "the owner cannot be removed")
		}
		if _, err := store.RemoveMember(ctx, tx, userID); err != nil {
			return err
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "member.removed", TargetType: "user", TargetID: userID.String(),
			Metadata: map[string]any{"role": string(old)}})
	})
}

// InviteEmailArgs mails an invitation link. The link waits sealed in
// Redis; the job table never sees it.
type InviteEmailArgs struct {
	jobs.TenantArgs
	InvitationID ulid.ID `json:"invitation_id"`
}

// Kind implements river.JobArgs.
func (InviteEmailArgs) Kind() string { return "identity.invitation.email" }

// NewDeviceAlertArgs warns a user about a sign-in from a new device.
type NewDeviceAlertArgs struct {
	jobs.GlobalArgs
	UserID    ulid.ID   `json:"user_id"`
	UserAgent string    `json:"user_agent"`
	IPPrefix  string    `json:"ip_prefix"`
	At        time.Time `json:"at"`
}

// Kind implements river.JobArgs.
func (NewDeviceAlertArgs) Kind() string { return "identity.device.alert" }
