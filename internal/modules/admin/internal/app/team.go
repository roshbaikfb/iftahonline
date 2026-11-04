package app

import (
	"context"
	"errors"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/authtoken"
	"github.com/roshbaikfb/iftahonline/internal/platform/email"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/totp"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// The console's team (ADR-0078): an owner invites admins, changes their
// roles, disables them and resets the ones who lost their authenticator.
// Each change is recorded first in the admin log with its reason. The
// owner never sees a password or a TOTP seed: the invitee sets both from
// a one-time link.

// InviteTTL is how long an invite or reset link works.
const InviteTTL = 72 * time.Hour

var (
	errNotYourself  = errs.Conflict("not_yourself", "you cannot change your own role or access; ask another owner")
	errLastOwner    = errs.Conflict("last_owner", "the console must keep at least one active owner")
	errAdminMissing = errs.NotFound("admin_not_found", "no admin with this ID")
	errInviteGone   = errs.NotFound("invite_not_found", "no open invite with this ID")
	errInviteBad    = errs.NotFound("invite_invalid", "this invite link has expired or was already used")
	errAdminExists  = errs.Conflict("admin_exists", "an admin or an open invite already has this email")
)

// TeamMember is an admin as the team page shows it.
type TeamMember = store.Member

// TeamInvite is an invite or a reset link.
type TeamInvite = store.Invite

// Team is the team page: every admin, and the invites not joined yet.
type Team struct {
	Members []TeamMember
	Invites []TeamInvite
}

// Team lists the console's admins and open invites.
func (s *Service) Team(ctx context.Context) (Team, error) {
	if _, ok := IdentityFrom(ctx); !ok {
		return Team{}, errNoSession
	}
	members, err := store.Team(ctx, s.d.DB)
	if err != nil {
		return Team{}, err
	}
	invites, err := store.OpenInvites(ctx, s.d.DB)
	if err != nil {
		return Team{}, err
	}
	return Team{Members: members, Invites: invites}, nil
}

// InviteLink is an invite and its link's token, shown once: only its hash
// is kept.
type InviteLink struct {
	Invite store.Invite
	Token  string
}

// InviteAdmin invites a new admin in role.
func (s *Service) InviteAdmin(ctx context.Context, rawEmail, rawName string, role domain.Role, rawReason string) (InviteLink, error) {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return InviteLink{}, errNoSession
	}
	addr, ok := email.Normalize(rawEmail)
	if !ok {
		return InviteLink{}, errs.Validation(errs.FieldError{Field: "email", Code: "invalid", Message: "invalid email"})
	}
	name, err := domain.ValidateName(rawName)
	if err != nil {
		return InviteLink{}, err
	}
	if _, err = domain.ParseRole(string(role)); err != nil {
		return InviteLink{}, err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return InviteLink{}, err
	}
	switch _, err = store.AdminByEmail(ctx, s.d.DB, addr); {
	case err == nil:
		return InviteLink{}, errAdminExists
	case !errors.Is(err, domain.ErrNotFound):
		return InviteLink{}, err
	}
	inv := store.Invite{ID: s.d.IDs.New(), AdminID: s.d.IDs.New(), Purpose: "join", Email: addr, Name: name, Role: role, InvitedBy: who.AdminID}
	if err = s.audit(ctx, &who.AdminID, "admin.invited", "admin", inv.AdminID.String(),
		map[string]any{"reason": reason, "name": name, "role": string(role)}); err != nil {
		return InviteLink{}, err
	}
	return s.issue(ctx, inv)
}

// RenewInvite replaces an open invite's link (the first was lost or
// expired): the old one stops working.
func (s *Service) RenewInvite(ctx context.Context, id ulid.ID, rawReason string) (InviteLink, error) {
	who, inv, reason, err := s.openInvite(ctx, id, rawReason)
	if err != nil {
		return InviteLink{}, err
	}
	if err = s.audit(ctx, &who.AdminID, "admin.invite_renewed", "admin", inv.AdminID.String(),
		map[string]any{"reason": reason, "name": inv.Name}); err != nil {
		return InviteLink{}, err
	}
	inv.ID, inv.InvitedBy = s.d.IDs.New(), who.AdminID
	return s.issue(ctx, inv)
}

// CancelInvite closes an open invite.
func (s *Service) CancelInvite(ctx context.Context, id ulid.ID, rawReason string) error {
	who, inv, reason, err := s.openInvite(ctx, id, rawReason)
	if err != nil {
		return err
	}
	if err = s.audit(ctx, &who.AdminID, "admin.invite_cancelled", "admin", inv.AdminID.String(),
		map[string]any{"reason": reason, "name": inv.Name}); err != nil {
		return err
	}
	if done, err := store.CancelInvite(ctx, s.d.DB, inv.AdminID); err != nil || !done {
		return cmpErr(err, errInviteGone)
	}
	return nil
}

func (s *Service) openInvite(ctx context.Context, id ulid.ID, rawReason string) (Identity, store.Invite, string, error) {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return Identity{}, store.Invite{}, "", errNoSession
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return Identity{}, store.Invite{}, "", err
	}
	inv, err := store.OpenInvite(ctx, s.d.DB, id)
	if errors.Is(err, domain.ErrNotFound) || err == nil && inv.Purpose != "join" {
		return Identity{}, store.Invite{}, "", errInviteGone
	}
	return who, inv, reason, err
}

// issue gives inv a fresh token and TOTP seed (sealed for the admin it
// makes) and records it, closing the admin's earlier link.
func (s *Service) issue(ctx context.Context, inv store.Invite) (InviteLink, error) {
	token, err := authtoken.New()
	if err != nil {
		return InviteLink{}, err
	}
	seed, err := totp.NewSecret()
	if err != nil {
		return InviteLink{}, err
	}
	if inv.TOTPSecret, err = s.d.Keyring.Seal(seed, totpAAD(inv.AdminID)); err != nil {
		return InviteLink{}, err
	}
	inv.CreatedAt = s.now()
	inv.ExpiresAt = inv.CreatedAt.Add(InviteTTL)
	err = store.InTx(ctx, s.d.DB, func(db store.DB) error { return store.InsertInvite(ctx, db, inv, token.Hash()) })
	if errors.Is(err, store.ErrEmailTaken) {
		return InviteLink{}, errAdminExists
	}
	if err != nil {
		return InviteLink{}, err
	}
	return InviteLink{Invite: inv, Token: string(token)}, nil
}

// ChangeRole moves another admin to role, keeping an active owner.
func (s *Service) ChangeRole(ctx context.Context, id ulid.ID, role domain.Role, rawReason string) error {
	who, target, reason, err := s.other(ctx, id, rawReason)
	if err != nil {
		return err
	}
	if _, err = domain.ParseRole(string(role)); err != nil {
		return err
	}
	if target.Role == role {
		return nil
	}
	if err = s.audit(ctx, &who.AdminID, "admin.role_changed", "admin", id.String(),
		map[string]any{"reason": reason, "name": target.Name, "from": string(target.Role), "to": string(role)}); err != nil {
		return err
	}
	return store.InTx(ctx, s.d.DB, func(db store.DB) error {
		if role != domain.RoleOwner {
			if err := keepOwner(ctx, db, id); err != nil {
				return err
			}
		}
		return store.SetRole(ctx, db, id, role)
	})
}

// SetAdminActive disables another admin (its sessions end at once) or
// enables it again.
func (s *Service) SetAdminActive(ctx context.Context, id ulid.ID, active bool, rawReason string) error {
	who, target, reason, err := s.other(ctx, id, rawReason)
	if err != nil {
		return err
	}
	action := "admin.disabled"
	if active {
		action = "admin.enabled"
	}
	if err = s.audit(ctx, &who.AdminID, action, "admin", id.String(), map[string]any{"reason": reason, "name": target.Name}); err != nil {
		return err
	}
	return store.InTx(ctx, s.d.DB, func(db store.DB) error {
		if !active {
			if err := keepOwner(ctx, db, id); err != nil {
				return err
			}
			if _, err := store.CancelInvite(ctx, db, id); err != nil { // a pending reset ends too
				return err
			}
		}
		return store.SetActive(ctx, db, id, active)
	})
}

// ResetAdmin locks another admin out (sessions end, password and code
// stop working) and gives a link to set new ones: for a lost phone.
func (s *Service) ResetAdmin(ctx context.Context, id ulid.ID, rawReason string) (InviteLink, error) {
	who, target, reason, err := s.other(ctx, id, rawReason)
	if err != nil {
		return InviteLink{}, err
	}
	if err = s.audit(ctx, &who.AdminID, "admin.reset", "admin", id.String(), map[string]any{"reason": reason, "name": target.Name}); err != nil {
		return InviteLink{}, err
	}
	if err = store.InTx(ctx, s.d.DB, func(db store.DB) error {
		if ownerErr := keepOwner(ctx, db, id); ownerErr != nil {
			return ownerErr
		}
		return store.SetActive(ctx, db, id, false)
	}); err != nil {
		return InviteLink{}, err
	}
	return s.issue(ctx, store.Invite{ID: s.d.IDs.New(), AdminID: id, Purpose: "reset", Email: target.Email, Name: target.Name, Role: target.Role,
		InvitedBy: who.AdminID})
}

// other is the signed-in admin, another admin id it acts on, and the
// reason; nobody changes their own role or access.
func (s *Service) other(ctx context.Context, id ulid.ID, rawReason string) (Identity, domain.Admin, string, error) {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return Identity{}, domain.Admin{}, "", errNoSession
	}
	if id == who.AdminID {
		return Identity{}, domain.Admin{}, "", errNotYourself
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return Identity{}, domain.Admin{}, "", err
	}
	target, err := store.AdminByID(ctx, s.d.DB, id)
	if errors.Is(err, domain.ErrNotFound) {
		return Identity{}, domain.Admin{}, "", errAdminMissing
	}
	return who, target, reason, err
}

// keepOwner refuses a change that would leave no active owner once admin
// id stops being one; the owners are locked so two owners cannot demote
// each other at once.
func keepOwner(ctx context.Context, db store.DB, id ulid.ID) error {
	owners, err := store.LockOwners(ctx, db)
	if err != nil {
		return err
	}
	if !domain.LeavesAnOwner(owners, id) {
		return errLastOwner
	}
	return nil
}

func cmpErr(err, otherwise error) error {
	if err != nil {
		return err
	}
	return otherwise
}

// InviteView is what an invite link shows its invitee: who invited them,
// to what, and the authenticator seed to scan.
type InviteView struct {
	Purpose         string
	Name, Email     string
	Role            domain.Role
	InvitedByName   string
	ExpiresAt       time.Time
	Secret, URI, QR string // the seed in base32, as otpauth://, and as an SVG QR code
}

// LookupInvite reads an open invite by its link. Any other link (unknown,
// used, cancelled, expired) is the same 404, telling nothing.
func (s *Service) LookupInvite(ctx context.Context, raw string) (InviteView, error) {
	inv, seed, err := s.inviteByLink(ctx, raw)
	if err != nil {
		return InviteView{}, err
	}
	uri := totp.URI(seed, TOTPIssuer, inv.Email)
	svg, err := qrSVG(uri)
	if err != nil {
		return InviteView{}, err
	}
	return InviteView{Purpose: inv.Purpose, Name: inv.Name, Email: inv.Email, Role: inv.Role, InvitedByName: inv.InvitedByName,
		ExpiresAt: inv.ExpiresAt, Secret: totp.EncodeSecret(seed), URI: uri, QR: svg}, nil
}

// AcceptInvite spends an invite: the invitee's password and a current
// code from the scanned seed make the admin (or reset it), then sign it
// in. The link works once.
func (s *Service) AcceptInvite(ctx context.Context, raw, pw, code string) (LoginResult, error) {
	inv, seed, err := s.inviteByLink(ctx, raw)
	if err != nil {
		return LoginResult{}, err
	}
	if err = domain.ValidatePassword(pw); err != nil {
		return LoginResult{}, err
	}
	step, ok := totp.Verify(seed, code, s.now(), 0)
	if !ok {
		return LoginResult{}, errs.Validation(errs.FieldError{Field: "code", Code: "invalid", Message: "the code does not match the authenticator; scan the QR code again"})
	}
	hash, err := s.d.Hasher.Hash(ctx, pw)
	if err != nil {
		return LoginResult{}, err
	}
	action := "admin.joined"
	if inv.Purpose == "reset" {
		action = "admin.reset_completed"
	}
	err = store.InTx(ctx, s.d.DB, func(db store.DB) error {
		used, txErr := store.UseInvite(ctx, db, inv.ID)
		if txErr != nil {
			return txErr
		}
		if !used {
			return errInviteBad
		}
		if inv.Purpose == "reset" {
			txErr = store.ResetCredentials(ctx, db, inv.AdminID, hash, inv.TOTPSecret)
		} else {
			txErr = store.InsertInvitedAdmin(ctx, db, inv, hash)
		}
		if errors.Is(txErr, store.ErrEmailTaken) {
			return errAdminExists
		}
		if txErr != nil {
			return txErr
		}
		// The code just used signs nobody in again.
		_, txErr = store.UseTOTPStep(ctx, db, inv.AdminID, step)
		return txErr
	})
	if err != nil {
		return LoginResult{}, err
	}
	if err = s.audit(ctx, &inv.AdminID, action, "admin", inv.AdminID.String(), map[string]any{"role": string(inv.Role)}); err != nil {
		return LoginResult{}, err
	}
	a, err := store.AdminByID(ctx, s.d.DB, inv.AdminID)
	if err != nil {
		return LoginResult{}, err
	}
	return s.openSession(ctx, a)
}

// inviteByLink is the open invite behind a link and its TOTP seed. Tries
// are throttled per address, like logins.
func (s *Service) inviteByLink(ctx context.Context, raw string) (store.Invite, []byte, error) {
	ip := "unknown"
	if a := metaFrom(ctx).IP; a.IsValid() {
		ip = a.String()
	}
	if err := s.allow(ctx, "invite:ip:"+ip, s.d.Limits.LoginPerIP, true); err != nil {
		return store.Invite{}, nil, err
	}
	t, ok := authtoken.Parse(raw)
	if !ok {
		return store.Invite{}, nil, errInviteBad
	}
	inv, err := store.InviteByToken(ctx, s.d.DB, t.Hash())
	if errors.Is(err, domain.ErrNotFound) || err == nil && (inv.Used || inv.Cancelled || !s.now().Before(inv.ExpiresAt)) {
		return store.Invite{}, nil, errInviteBad
	}
	if err != nil {
		return store.Invite{}, nil, err
	}
	seed, err := s.d.Keyring.Open(inv.TOTPSecret, totpAAD(inv.AdminID))
	if err != nil {
		return store.Invite{}, nil, err
	}
	return inv, seed, nil
}
