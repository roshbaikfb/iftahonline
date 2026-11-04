package httpapi

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var (
	errAdminNotFound  = errs.NotFound("admin_not_found", "no admin with this ID")
	errInviteNotFound = errs.NotFound("invite_not_found", "no open invite with this ID")
)

// AdminGetTeam implements StrictServerInterface.
func (h *Handlers) AdminGetTeam(ctx context.Context, _ AdminGetTeamRequestObject) (AdminGetTeamResponseObject, error) {
	t, err := h.svc.Team(ctx)
	if err != nil {
		return nil, err
	}
	out := AdminGetTeam200JSONResponse{Admins: make([]AdminTeamMember, len(t.Members)), Invites: make([]AdminInvite, len(t.Invites))}
	for i, m := range t.Members {
		out.Admins[i] = toMember(m)
	}
	for i, inv := range t.Invites {
		out.Invites[i] = toInvite(inv)
	}
	return out, nil
}

func toInvite(inv store.Invite) AdminInvite {
	out := AdminInvite{Id: inv.ID.String(), Purpose: AdminInvitePurpose(inv.Purpose), AdminId: inv.AdminID.String(), Email: inv.Email,
		Name: inv.Name, Role: AdminRole(inv.Role), CreatedAt: inv.CreatedAt, ExpiresAt: inv.ExpiresAt}
	if inv.InvitedByName != "" {
		n := inv.InvitedByName
		out.InvitedByName = &n
	}
	return out
}

func toLink(l app.InviteLink) AdminInviteLink {
	return AdminInviteLink{Invite: toInvite(l.Invite), Token: l.Token}
}

// AdminInviteAdmin implements StrictServerInterface.
func (h *Handlers) AdminInviteAdmin(ctx context.Context, req AdminInviteAdminRequestObject) (AdminInviteAdminResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	b := req.Body
	l, err := h.svc.InviteAdmin(ctx, b.Email, b.Name, domain.Role(b.Role), b.Reason)
	if err != nil {
		return nil, err
	}
	return AdminInviteAdmin201JSONResponse(toLink(l)), nil
}

// AdminRenewInvite implements StrictServerInterface.
func (h *Handlers) AdminRenewInvite(ctx context.Context, req AdminRenewInviteRequestObject) (AdminRenewInviteResponseObject, error) {
	id, err := ulid.Parse(req.InviteId)
	if err != nil {
		return nil, errInviteNotFound
	}
	if req.Body == nil {
		return nil, errBody
	}
	l, err := h.svc.RenewInvite(ctx, id, req.Body.Reason)
	if err != nil {
		return nil, err
	}
	return AdminRenewInvite200JSONResponse(toLink(l)), nil
}

// AdminCancelInvite implements StrictServerInterface.
func (h *Handlers) AdminCancelInvite(ctx context.Context, req AdminCancelInviteRequestObject) (AdminCancelInviteResponseObject, error) {
	id, err := ulid.Parse(req.InviteId)
	if err != nil {
		return nil, errInviteNotFound
	}
	if req.Body == nil {
		return nil, errBody
	}
	if err = h.svc.CancelInvite(ctx, id, req.Body.Reason); err != nil {
		return nil, err
	}
	return AdminCancelInvite204Response{}, nil
}

// AdminSetRole implements StrictServerInterface.
func (h *Handlers) AdminSetRole(ctx context.Context, req AdminSetRoleRequestObject) (AdminSetRoleResponseObject, error) {
	id, err := ulid.Parse(req.AdminId)
	if err != nil {
		return nil, errAdminNotFound
	}
	if req.Body == nil {
		return nil, errBody
	}
	if err = h.svc.ChangeRole(ctx, id, domain.Role(req.Body.Role), req.Body.Reason); err != nil {
		return nil, err
	}
	return AdminSetRole204Response{}, nil
}

// AdminDisableAdmin implements StrictServerInterface.
func (h *Handlers) AdminDisableAdmin(ctx context.Context, req AdminDisableAdminRequestObject) (AdminDisableAdminResponseObject, error) {
	id, err := ulid.Parse(req.AdminId)
	if err != nil {
		return nil, errAdminNotFound
	}
	if req.Body == nil {
		return nil, errBody
	}
	if err = h.svc.SetAdminActive(ctx, id, false, req.Body.Reason); err != nil {
		return nil, err
	}
	return AdminDisableAdmin204Response{}, nil
}

// AdminEnableAdmin implements StrictServerInterface.
func (h *Handlers) AdminEnableAdmin(ctx context.Context, req AdminEnableAdminRequestObject) (AdminEnableAdminResponseObject, error) {
	id, err := ulid.Parse(req.AdminId)
	if err != nil {
		return nil, errAdminNotFound
	}
	if req.Body == nil {
		return nil, errBody
	}
	if err = h.svc.SetAdminActive(ctx, id, true, req.Body.Reason); err != nil {
		return nil, err
	}
	return AdminEnableAdmin204Response{}, nil
}

// AdminResetAdmin implements StrictServerInterface.
func (h *Handlers) AdminResetAdmin(ctx context.Context, req AdminResetAdminRequestObject) (AdminResetAdminResponseObject, error) {
	id, err := ulid.Parse(req.AdminId)
	if err != nil {
		return nil, errAdminNotFound
	}
	if req.Body == nil {
		return nil, errBody
	}
	l, err := h.svc.ResetAdmin(ctx, id, req.Body.Reason)
	if err != nil {
		return nil, err
	}
	return AdminResetAdmin200JSONResponse(toLink(l)), nil
}

// AdminLookupInvite implements StrictServerInterface. No session: Scope
// lets the invite routes through.
func (h *Handlers) AdminLookupInvite(ctx context.Context, req AdminLookupInviteRequestObject) (AdminLookupInviteResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	v, err := h.svc.LookupInvite(ctx, req.Body.Token)
	if err != nil {
		return nil, err
	}
	return AdminLookupInvite200JSONResponse{Purpose: AdminInviteViewPurpose(v.Purpose), Name: v.Name, Email: v.Email, Role: AdminRole(v.Role),
		InvitedByName: v.InvitedByName, ExpiresAt: v.ExpiresAt, TotpSecret: v.Secret, TotpUri: v.URI, TotpQrSvg: v.QR}, nil
}

// AdminAcceptInvite implements StrictServerInterface: it signs the new
// admin in, as a login does.
func (h *Handlers) AdminAcceptInvite(ctx context.Context, req AdminAcceptInviteRequestObject) (AdminAcceptInviteResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	res, err := h.svc.AcceptInvite(ctx, req.Body.Token, req.Body.Password, req.Body.Code)
	if err != nil {
		return nil, err
	}
	body, cookie := signedIn(res)
	return AdminAcceptInvite200JSONResponse{Body: body, Headers: AdminAcceptInvite200ResponseHeaders{SetCookie: &cookie}}, nil
}
