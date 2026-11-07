package app

import (
	"context"
	"errors"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/mailer"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// A store's team on the console (C1, the approved mockups): who works on
// the store, and platform support's actions on their accounts. Identity
// owns the accounts; cmd/platform adapts it to StoreSupport, so neither
// module imports the other. Every action records the admin, the reason
// and the store in the admin log first, then identity records it in the
// store's activity log.

// StoreMember is a member of a store's team.
type StoreMember struct {
	UserID     ulid.ID
	Name       string
	Email      string // empty for a role without contacts.read
	Phone      string // masked; empty when none or without contacts.read
	Role       string
	MFAEnabled bool
	Disabled   bool
	JoinedAt   time.Time
	LastSeenAt time.Time // zero: never
}

// StoreInvite is a pending invitation to a store.
type StoreInvite struct {
	Email     string // empty for a role without contacts.read
	Role      string
	ExpiresAt time.Time
}

// SupportRequest is a delayed change support asked for in a store.
type SupportRequest struct {
	ID          ulid.ID
	Kind        string
	UserID      ulid.ID
	RequestedBy ulid.ID
	Reason      string
	CreatedAt   time.Time
	EffectiveAt time.Time
	CancelledAt *time.Time
	CancelledBy string
	DoneAt      *time.Time
	// RequesterName and RequesterRole name the admin who asked (empty
	// when the admin is gone).
	RequesterName string
	RequesterRole domain.Role
}

// StoreTeam is a store's team as identity answers it.
type StoreTeam struct {
	Members  []StoreMember
	Invites  []StoreInvite
	Requests []SupportRequest
}

// MemberStore is a store a member's account works on.
type MemberStore struct {
	StoreID ulid.ID
	Name    string
	Role    string
}

// StoreSupport is identity's support operations on the store in ctx
// (wired in cmd/platform).
type StoreSupport interface {
	Team(ctx context.Context) (StoreTeam, error)
	RequestMFAReset(ctx context.Context, admin, user ulid.ID, reason string) (SupportRequest, error)
	CancelRequest(ctx context.Context, admin, id ulid.ID, reason string) error
	SetDisabled(ctx context.Context, admin, user ulid.ID, disabled bool, reason string) error
	SignOut(ctx context.Context, admin, user ulid.ID, reason string) error
	// MemberStores lists every store user works on (at most 20).
	MemberStores(ctx context.Context, user ulid.ID) ([]MemberStore, error)
}

var (
	errNoSupport = errs.Unavailable(0, errors.New("admin: store support is not wired"))
	errNotMember = errs.NotFound("member_not_found", "this account is not a member of the store")
)

// support is the signed-in admin and the store id's context, after
// checking the store exists (404 otherwise).
func (s *Service) support(ctx context.Context, id tenant.StoreID) (Identity, context.Context, error) {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return Identity{}, nil, errNoSession
	}
	if s.d.Support == nil {
		return Identity{}, nil, errNoSupport
	}
	if _, _, err := s.d.Directory.Store(ctx, id); err != nil {
		return Identity{}, nil, err
	}
	return who, tenant.WithStore(ctx, id), nil
}

// StoreTeam is store id's team: its members, pending invitations, and
// support requests open or closed lately, each naming the admin who
// asked. Contacts are left out for a role without contacts.read.
func (s *Service) StoreTeam(ctx context.Context, id tenant.StoreID) (StoreTeam, error) {
	who, sctx, err := s.support(ctx, id)
	if err != nil {
		return StoreTeam{}, err
	}
	team, err := s.d.Support.Team(sctx)
	if err != nil {
		return StoreTeam{}, err
	}
	if !who.Can(domain.PermContactsRead) {
		for i := range team.Members {
			team.Members[i].Email, team.Members[i].Phone = "", ""
		}
		for i := range team.Invites {
			team.Invites[i].Email = ""
		}
	}
	if len(team.Requests) > 0 {
		admins, adminsErr := store.Team(ctx, s.d.DB)
		if adminsErr != nil {
			return StoreTeam{}, adminsErr
		}
		byID := make(map[ulid.ID]store.Member, len(admins))
		for _, a := range admins {
			byID[a.ID] = a
		}
		for i, r := range team.Requests {
			if a, ok := byID[r.RequestedBy]; ok {
				team.Requests[i].RequesterName, team.Requests[i].RequesterRole = a.Name, a.Role
			}
		}
	}
	return team, nil
}

// RequestMFAReset asks for member user's two-step verification to be
// removed in 24 hours (identity emails the member and the owner a link
// that cancels it).
func (s *Service) RequestMFAReset(ctx context.Context, id tenant.StoreID, user ulid.ID, rawReason string) (SupportRequest, error) {
	var req SupportRequest
	err := s.supportDo(ctx, id, rawReason, "store.member_mfa_reset_requested", "user_id", user,
		func(sctx context.Context, who Identity, reason string) error {
			var err error
			if req, err = s.d.Support.RequestMFAReset(sctx, who.AdminID, user, reason); err != nil {
				return err
			}
			me, err := store.AdminByID(ctx, s.d.DB, who.AdminID)
			req.RequesterName, req.RequesterRole = me.Name, me.Role
			return err
		})
	return req, err
}

// CancelSupportRequest cancels a request of store id that still waits.
func (s *Service) CancelSupportRequest(ctx context.Context, id tenant.StoreID, request ulid.ID, rawReason string) error {
	return s.supportDo(ctx, id, rawReason, "store.support_request_cancelled", "request_id", request,
		func(sctx context.Context, who Identity, reason string) error {
			return s.d.Support.CancelRequest(sctx, who.AdminID, request, reason)
		})
}

// SetMemberDisabled disables member user's account (in every store it
// works on; its sessions end at once) or enables it again.
func (s *Service) SetMemberDisabled(ctx context.Context, id tenant.StoreID, user ulid.ID, disabled bool, rawReason string) error {
	action := "store.member_enabled"
	if disabled {
		action = "store.member_disabled"
	}
	return s.supportDo(ctx, id, rawReason, action, "user_id", user, func(sctx context.Context, who Identity, reason string) error {
		return s.d.Support.SetDisabled(sctx, who.AdminID, user, disabled, reason)
	})
}

// SignOutMember ends every session of member user.
func (s *Service) SignOutMember(ctx context.Context, id tenant.StoreID, user ulid.ID, rawReason string) error {
	return s.supportDo(ctx, id, rawReason, "store.member_signed_out", "user_id", user, func(sctx context.Context, who Identity, reason string) error {
		return s.d.Support.SignOut(sctx, who.AdminID, user, reason)
	})
}

// supportDo runs a support action on store id: the reason checked, the
// action recorded in the admin log (with the target under key), then
// call in the store's ctx.
func (s *Service) supportDo(ctx context.Context, id tenant.StoreID, rawReason, action, key string, target ulid.ID,
	call func(sctx context.Context, who Identity, reason string) error,
) error {
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return err
	}
	who, sctx, err := s.support(ctx, id)
	if err != nil {
		return err
	}
	if err = s.audit(ctx, &who.AdminID, action, "store", id.String(), map[string]any{"reason": reason, key: target.String()}); err != nil {
		return err
	}
	return call(sctx, who, reason)
}

// MemberStores lists the stores besides id that member user works on,
// named, for the warning before an account is disabled.
func (s *Service) MemberStores(ctx context.Context, id tenant.StoreID, user ulid.ID) ([]MemberStore, error) {
	_, sctx, err := s.support(ctx, id)
	if err != nil {
		return nil, err
	}
	all, err := s.d.Support.MemberStores(sctx, user)
	if err != nil {
		return nil, err
	}
	others := make([]MemberStore, 0, len(all))
	ids := make([]ulid.ID, 0, len(all))
	member := false
	for _, m := range all {
		if m.StoreID == id.ID {
			member = true
			continue
		}
		others = append(others, m)
		ids = append(ids, m.StoreID)
	}
	if !member {
		return nil, errNotMember
	}
	if len(ids) == 0 {
		return others, nil
	}
	names, err := s.d.Directory.Names(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range others {
		others[i].Name = names[others[i].StoreID]
	}
	return others, nil
}

// NotifyAdmin emails admin id (an active one) from the worker: identity
// tells the admin who asked when a merchant cancels a support request.
// Without a mailer it sends nothing.
func (s *Service) NotifyAdmin(ctx context.Context, id ulid.ID, subject, text string) error {
	if s.d.Mailer == nil {
		return nil
	}
	a, err := store.AdminByID(ctx, s.d.DB, id)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !a.Active {
		return nil
	}
	if s.d.ConsoleURL != "" {
		text += "\n\n" + s.d.ConsoleURL
	}
	return s.d.Mailer.Send(ctx, mailer.Message{To: a.Email, Subject: subject, Text: text})
}
