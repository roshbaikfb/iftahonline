package httpapi

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// A store's team and platform support's actions on it (C1).

var (
	errMemberNotFound  = errs.NotFound("member_not_found", "this account is not a member of the store")
	errRequestNotFound = errs.NotFound("request_not_found", "this request was cancelled, done, or does not exist")
)

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func toSupportRequest(r app.SupportRequest) AdminSupportRequest {
	out := AdminSupportRequest{Id: r.ID.String(), Kind: AdminSupportRequestKind(r.Kind), UserId: r.UserID.String(), RequestedBy: r.RequestedBy.String(),
		RequestedByName: r.RequesterName, Reason: r.Reason, CreatedAt: r.CreatedAt, EffectiveAt: r.EffectiveAt, State: AdminSupportRequestStateOpen}
	if r.RequesterRole != "" {
		role := AdminRole(r.RequesterRole)
		out.RequestedByRole = &role
	}
	switch {
	case r.CancelledAt != nil:
		by := AdminSupportRequestCancelledBy(r.CancelledBy)
		out.State, out.CancelledBy, out.ClosedAt = AdminSupportRequestStateCancelled, &by, r.CancelledAt
	case r.DoneAt != nil:
		out.State, out.ClosedAt = AdminSupportRequestStateDone, r.DoneAt
	}
	return out
}

// AdminGetStoreTeam implements StrictServerInterface.
func (h *Handlers) AdminGetStoreTeam(ctx context.Context, req AdminGetStoreTeamRequestObject) (AdminGetStoreTeamResponseObject, error) {
	id, err := storeID(req.StoreId)
	if err != nil {
		return nil, err
	}
	t, err := h.svc.StoreTeam(ctx, id)
	if err != nil {
		return nil, err
	}
	out := AdminGetStoreTeam200JSONResponse{Members: make([]AdminStoreMember, len(t.Members)), Invites: make([]AdminStoreInvite, len(t.Invites)),
		Requests: make([]AdminSupportRequest, len(t.Requests))}
	for i, m := range t.Members {
		out.Members[i] = AdminStoreMember{UserId: m.UserID.String(), Name: m.Name, Email: optional(m.Email), Phone: optional(m.Phone),
			Role: AdminStoreMemberRole(m.Role), MfaEnabled: m.MFAEnabled, Disabled: m.Disabled, JoinedAt: m.JoinedAt}
		if !m.LastSeenAt.IsZero() {
			seen := m.LastSeenAt
			out.Members[i].LastSeenAt = &seen
		}
	}
	for i, inv := range t.Invites {
		out.Invites[i] = AdminStoreInvite{Email: optional(inv.Email), Role: AdminStoreInviteRole(inv.Role), ExpiresAt: inv.ExpiresAt}
	}
	for i, r := range t.Requests {
		out.Requests[i] = toSupportRequest(r)
	}
	return out, nil
}

// memberTarget parses a member action's store and user.
func memberTarget(rawStore, rawUser string, body bool) (tenant.StoreID, ulid.ID, error) {
	id, err := storeID(rawStore)
	if err != nil {
		return tenant.StoreID{}, ulid.ID{}, err
	}
	user, err := ulid.Parse(rawUser)
	if err != nil {
		return tenant.StoreID{}, ulid.ID{}, errMemberNotFound
	}
	if !body {
		return tenant.StoreID{}, ulid.ID{}, errBody
	}
	return id, user, nil
}

// AdminRequestMemberMFAReset implements StrictServerInterface.
func (h *Handlers) AdminRequestMemberMFAReset(ctx context.Context, req AdminRequestMemberMFAResetRequestObject) (AdminRequestMemberMFAResetResponseObject, error) {
	id, user, err := memberTarget(req.StoreId, req.UserId, req.Body != nil)
	if err != nil {
		return nil, err
	}
	r, err := h.svc.RequestMFAReset(ctx, id, user, req.Body.Reason)
	if err != nil {
		return nil, err
	}
	return AdminRequestMemberMFAReset202JSONResponse(toSupportRequest(r)), nil
}

// AdminRequestOwnershipTransfer implements StrictServerInterface (C2).
func (h *Handlers) AdminRequestOwnershipTransfer(ctx context.Context, req AdminRequestOwnershipTransferRequestObject) (AdminRequestOwnershipTransferResponseObject, error) {
	id, user, err := memberTarget(req.StoreId, req.UserId, req.Body != nil)
	if err != nil {
		return nil, err
	}
	r, err := h.svc.RequestOwnershipTransfer(ctx, id, user, req.Body.Reason)
	if err != nil {
		return nil, err
	}
	return AdminRequestOwnershipTransfer202JSONResponse(toSupportRequest(r)), nil
}

// AdminDisableMember implements StrictServerInterface.
func (h *Handlers) AdminDisableMember(ctx context.Context, req AdminDisableMemberRequestObject) (AdminDisableMemberResponseObject, error) {
	id, user, err := memberTarget(req.StoreId, req.UserId, req.Body != nil)
	if err != nil {
		return nil, err
	}
	if err = h.svc.SetMemberDisabled(ctx, id, user, true, req.Body.Reason); err != nil {
		return nil, err
	}
	return AdminDisableMember204Response{}, nil
}

// AdminEnableMember implements StrictServerInterface.
func (h *Handlers) AdminEnableMember(ctx context.Context, req AdminEnableMemberRequestObject) (AdminEnableMemberResponseObject, error) {
	id, user, err := memberTarget(req.StoreId, req.UserId, req.Body != nil)
	if err != nil {
		return nil, err
	}
	if err = h.svc.SetMemberDisabled(ctx, id, user, false, req.Body.Reason); err != nil {
		return nil, err
	}
	return AdminEnableMember204Response{}, nil
}

// AdminSignOutMember implements StrictServerInterface.
func (h *Handlers) AdminSignOutMember(ctx context.Context, req AdminSignOutMemberRequestObject) (AdminSignOutMemberResponseObject, error) {
	id, user, err := memberTarget(req.StoreId, req.UserId, req.Body != nil)
	if err != nil {
		return nil, err
	}
	if err = h.svc.SignOutMember(ctx, id, user, req.Body.Reason); err != nil {
		return nil, err
	}
	return AdminSignOutMember204Response{}, nil
}

// AdminGetMemberStores implements StrictServerInterface.
func (h *Handlers) AdminGetMemberStores(ctx context.Context, req AdminGetMemberStoresRequestObject) (AdminGetMemberStoresResponseObject, error) {
	id, user, err := memberTarget(req.StoreId, req.UserId, true)
	if err != nil {
		return nil, err
	}
	stores, err := h.svc.MemberStores(ctx, id, user)
	if err != nil {
		return nil, err
	}
	out := AdminGetMemberStores200JSONResponse{Items: make([]struct {
		Name    string                     `json:"name"`
		Role    AdminMemberStoresItemsRole `json:"role"`
		StoreId ULID                       `json:"store_id"`
	}, len(stores))}
	for i, s := range stores {
		out.Items[i].Name, out.Items[i].Role, out.Items[i].StoreId = s.Name, AdminMemberStoresItemsRole(s.Role), s.StoreID.String()
	}
	return out, nil
}

// AdminCancelSupportRequest implements StrictServerInterface.
func (h *Handlers) AdminCancelSupportRequest(ctx context.Context, req AdminCancelSupportRequestRequestObject) (AdminCancelSupportRequestResponseObject, error) {
	id, err := storeID(req.StoreId)
	if err != nil {
		return nil, err
	}
	request, err := ulid.Parse(req.RequestId)
	if err != nil {
		return nil, errRequestNotFound
	}
	if req.Body == nil {
		return nil, errBody
	}
	if err = h.svc.CancelSupportRequest(ctx, id, request, req.Body.Reason); err != nil {
		return nil, err
	}
	return AdminCancelSupportRequest204Response{}, nil
}
