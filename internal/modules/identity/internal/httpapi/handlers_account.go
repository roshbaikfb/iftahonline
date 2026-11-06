package httpapi

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// The account page (D4).

// UpdateMe implements StrictServerInterface.
func (h *Handlers) UpdateMe(ctx context.Context, req UpdateMeRequestObject) (UpdateMeResponseObject, error) {
	if req.Body == nil {
		return nil, errs.BadRequest("request body is required", nil)
	}
	u, err := h.svc.UpdateName(ctx, req.Body.Name)
	if err != nil {
		return nil, err
	}
	return UpdateMe200JSONResponse(toUser(u)), nil
}

// ChangePassword implements StrictServerInterface.
func (h *Handlers) ChangePassword(ctx context.Context, req ChangePasswordRequestObject) (ChangePasswordResponseObject, error) {
	if req.Body == nil {
		return nil, errs.BadRequest("request body is required", nil)
	}
	if err := h.svc.ChangePassword(ctx, req.Body.CurrentPassword, req.Body.NewPassword); err != nil {
		return nil, err
	}
	return ChangePassword204Response{}, nil
}
