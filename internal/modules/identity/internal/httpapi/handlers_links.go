package httpapi

import (
	"context"
	"fmt"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// The one-time email links (D3): a forgotten password and email
// confirmation.

// ForgotPassword implements StrictServerInterface: the same answer whether
// the email has an account or not.
func (h *Handlers) ForgotPassword(ctx context.Context, req ForgotPasswordRequestObject) (ForgotPasswordResponseObject, error) {
	if req.Body == nil {
		return nil, errs.BadRequest("request body is required", nil)
	}
	if err := h.svc.ForgotPassword(ctx, req.Body.Email); err != nil {
		return nil, err
	}
	return ForgotPassword202Response{}, nil
}

// ResetPassword implements StrictServerInterface: it signs in like a login.
func (h *Handlers) ResetPassword(ctx context.Context, req ResetPasswordRequestObject) (ResetPasswordResponseObject, error) {
	if req.Body == nil {
		return nil, errs.BadRequest("request body is required", nil)
	}
	res, err := h.svc.ResetPassword(ctx, req.Body.Token, req.Body.Password)
	out, err := signIn(res, err, req.Body.Client)
	if err != nil {
		return nil, err
	}
	// The same answer as a login's, under this operation's types.
	switch v := out.(type) {
	case Login202JSONResponse:
		return ResetPassword202JSONResponse(v), nil
	case Login200JSONResponse:
		return ResetPassword200JSONResponse{Body: v.Body, Headers: ResetPassword200ResponseHeaders(v.Headers)}, nil
	}
	return nil, fmt.Errorf("identity: unexpected sign-in answer %T", out)
}

// VerifyEmail implements StrictServerInterface.
func (h *Handlers) VerifyEmail(ctx context.Context, req VerifyEmailRequestObject) (VerifyEmailResponseObject, error) {
	if req.Body == nil {
		return nil, errs.BadRequest("request body is required", nil)
	}
	email, err := h.svc.VerifyEmail(ctx, req.Body.Token)
	if err != nil {
		return nil, err
	}
	return VerifyEmail200JSONResponse{Email: email}, nil
}

// CheckResetLink implements StrictServerInterface.
func (h *Handlers) CheckResetLink(ctx context.Context, req CheckResetLinkRequestObject) (CheckResetLinkResponseObject, error) {
	if req.Body == nil {
		return nil, errs.BadRequest("request body is required", nil)
	}
	email, err := h.svc.CheckResetLink(ctx, req.Body.Token)
	if err != nil {
		return nil, err
	}
	return CheckResetLink200JSONResponse{Email: email}, nil
}

// SendEmailVerification implements StrictServerInterface.
func (h *Handlers) SendEmailVerification(ctx context.Context, _ SendEmailVerificationRequestObject) (SendEmailVerificationResponseObject, error) {
	if err := h.svc.SendEmailVerification(ctx); err != nil {
		return nil, err
	}
	return SendEmailVerification202Response{}, nil
}
