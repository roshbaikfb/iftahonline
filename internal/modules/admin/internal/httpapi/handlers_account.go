package httpapi

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// The signed-in admin's own account (C3).

// AdminUpdateMe implements StrictServerInterface.
func (h *Handlers) AdminUpdateMe(ctx context.Context, req AdminUpdateMeRequestObject) (AdminUpdateMeResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	a, err := h.svc.UpdateName(ctx, req.Body.Name)
	if err != nil {
		return nil, err
	}
	return AdminUpdateMe200JSONResponse(profile(a)), nil
}

// AdminChangePassword implements StrictServerInterface.
func (h *Handlers) AdminChangePassword(ctx context.Context, req AdminChangePasswordRequestObject) (AdminChangePasswordResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	if err := h.svc.ChangePassword(ctx, req.Body.CurrentPassword, req.Body.NewPassword); err != nil {
		return nil, err
	}
	return AdminChangePassword204Response{}, nil
}

// AdminStartNewPhone implements StrictServerInterface.
func (h *Handlers) AdminStartNewPhone(ctx context.Context, req AdminStartNewPhoneRequestObject) (AdminStartNewPhoneResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	p, err := h.svc.StartNewPhone(ctx, req.Body.Code)
	if err != nil {
		return nil, err
	}
	return AdminStartNewPhone200JSONResponse{Secret: p.Secret, Uri: p.URI, QrSvg: p.QR}, nil
}

// AdminConfirmNewPhone implements StrictServerInterface.
func (h *Handlers) AdminConfirmNewPhone(ctx context.Context, req AdminConfirmNewPhoneRequestObject) (AdminConfirmNewPhoneResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	if err := h.svc.ConfirmNewPhone(ctx, req.Body.Code); err != nil {
		return nil, err
	}
	return AdminConfirmNewPhone204Response{}, nil
}

// AdminListMySessions implements StrictServerInterface.
func (h *Handlers) AdminListMySessions(ctx context.Context, _ AdminListMySessionsRequestObject) (AdminListMySessionsResponseObject, error) {
	ss, err := h.svc.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	out := AdminListMySessions200JSONResponse{Items: make([]AdminOwnSession, len(ss))}
	for i, s := range ss {
		out.Items[i] = AdminOwnSession{Id: s.ID.String(), UserAgent: s.UserAgent, IpPrefix: s.IPPrefix, CreatedAt: s.CreatedAt, LastSeenAt: s.LastSeenAt,
			Current: s.Current}
	}
	return out, nil
}

// AdminRevokeMySession implements StrictServerInterface.
func (h *Handlers) AdminRevokeMySession(ctx context.Context, req AdminRevokeMySessionRequestObject) (AdminRevokeMySessionResponseObject, error) {
	id, err := ulid.Parse(req.SessionId)
	if err != nil {
		return nil, errs.NotFound("session_not_found", "no such session")
	}
	if err = h.svc.RevokeSession(ctx, id); err != nil {
		return nil, err
	}
	return AdminRevokeMySession204Response{}, nil
}

// AdminRevokeMyOtherSessions implements StrictServerInterface.
func (h *Handlers) AdminRevokeMyOtherSessions(ctx context.Context, _ AdminRevokeMyOtherSessionsRequestObject) (AdminRevokeMyOtherSessionsResponseObject, error) {
	if err := h.svc.RevokeOtherSessions(ctx); err != nil {
		return nil, err
	}
	return AdminRevokeMyOtherSessions204Response{}, nil
}
