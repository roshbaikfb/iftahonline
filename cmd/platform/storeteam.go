package main

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// storeSupport lets the console act on a store's team through identity
// (C1); neither module imports the other. MemberStores reads on the
// console's pool, through app.member_stores.
type storeSupport struct {
	id        *identity.Module
	adminPool *pgxpool.Pool
}

var _ admin.StoreSupport = storeSupport{}

func toSupportRequest(r identity.SupportRequest) admin.SupportRequest {
	return admin.SupportRequest{ID: r.ID, Kind: r.Kind, UserID: r.UserID, RequestedBy: r.RequestedBy, Reason: r.Reason, CreatedAt: r.CreatedAt,
		EffectiveAt: r.EffectiveAt, CancelledAt: r.CancelledAt, CancelledBy: r.CancelledBy, DoneAt: r.DoneAt}
}

func (s storeSupport) Team(ctx context.Context) (admin.StoreTeam, error) {
	t, err := s.id.SupportTeam(ctx)
	if err != nil {
		return admin.StoreTeam{}, err
	}
	out := admin.StoreTeam{Members: make([]admin.StoreMember, len(t.Members)), Invites: make([]admin.StoreInvite, len(t.Invites)),
		Requests: make([]admin.SupportRequest, len(t.Requests))}
	for i, m := range t.Members {
		out.Members[i] = admin.StoreMember{UserID: m.UserID, Name: m.Name, Email: m.Email, Phone: m.Phone, Role: string(m.Role),
			MFAEnabled: m.MFAEnabled, Disabled: m.Disabled, JoinedAt: m.JoinedAt, LastSeenAt: m.LastSeenAt}
	}
	for i, inv := range t.Invites {
		out.Invites[i] = admin.StoreInvite{Email: inv.Email, Role: string(inv.Role), ExpiresAt: inv.ExpiresAt}
	}
	for i, r := range t.Requests {
		out.Requests[i] = toSupportRequest(r)
	}
	return out, nil
}

func (s storeSupport) RequestMFAReset(ctx context.Context, by, user ulid.ID, reason string) (admin.SupportRequest, error) {
	r, err := s.id.RequestMFAReset(ctx, by, user, reason)
	return toSupportRequest(r), err
}

func (s storeSupport) RequestOwnershipTransfer(ctx context.Context, by, user ulid.ID, reason string) (admin.SupportRequest, error) {
	r, err := s.id.RequestOwnershipTransfer(ctx, by, user, reason)
	return toSupportRequest(r), err
}

func (s storeSupport) CancelRequest(ctx context.Context, by, id ulid.ID, reason string) error {
	return s.id.CancelSupportRequest(ctx, by, id, reason)
}

func (s storeSupport) SetDisabled(ctx context.Context, by, user ulid.ID, disabled bool, reason string) error {
	return s.id.SetMemberDisabled(ctx, by, user, disabled, reason)
}

func (s storeSupport) SignOut(ctx context.Context, by, user ulid.ID, reason string) error {
	return s.id.SignOutMember(ctx, by, user, reason)
}

func (s storeSupport) MemberStores(ctx context.Context, user ulid.ID) ([]admin.MemberStore, error) {
	rows, err := identity.MemberStores(ctx, s.adminPool, user)
	if err != nil {
		return nil, err
	}
	out := make([]admin.MemberStore, len(rows))
	for i, r := range rows {
		out[i] = admin.MemberStore{StoreID: r.StoreID, Role: string(r.Role)}
	}
	return out, nil
}
