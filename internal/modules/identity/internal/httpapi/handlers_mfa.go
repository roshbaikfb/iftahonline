package httpapi

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var errBodyRequired = errs.BadRequest("request body is required", nil)

func region(r *PhoneRequestRegion) string {
	if r == nil {
		return ""
	}
	return string(*r)
}

// VerifyMFA implements StrictServerInterface.
func (h *Handlers) VerifyMFA(ctx context.Context, req VerifyMFARequestObject) (VerifyMFAResponseObject, error) {
	if req.Body == nil {
		return nil, errBodyRequired
	}
	res, err := h.svc.VerifyMFA(ctx, req.Body.ChallengeId, req.Body.Code)
	if err != nil {
		return nil, err
	}
	body, cookie := authResponse(res, mobile(req.Body.Client))
	return VerifyMFA200JSONResponse{Body: body, Headers: VerifyMFA200ResponseHeaders{SetCookie: cookie}}, nil
}

// StartOTPLogin implements StrictServerInterface.
func (h *Handlers) StartOTPLogin(ctx context.Context, req StartOTPLoginRequestObject) (StartOTPLoginResponseObject, error) {
	if req.Body == nil {
		return nil, errBodyRequired
	}
	ch, err := h.svc.StartOTPLogin(ctx, req.Body.Phone, region(req.Body.Region))
	if err != nil {
		return nil, err
	}
	return StartOTPLogin202JSONResponse{ChallengeId: ch.ID, ExpiresAt: ch.ExpiresAt}, nil
}

// VerifyOTPLogin implements StrictServerInterface.
func (h *Handlers) VerifyOTPLogin(ctx context.Context, req VerifyOTPLoginRequestObject) (VerifyOTPLoginResponseObject, error) {
	if req.Body == nil {
		return nil, errBodyRequired
	}
	res, err := h.svc.VerifyOTPLogin(ctx, req.Body.ChallengeId, req.Body.Code)
	if err != nil {
		return nil, err
	}
	if res.MFA != nil {
		return VerifyOTPLogin202JSONResponse(mfaRequired(res.MFA)), nil
	}
	body, cookie := authResponse(res, mobile(req.Body.Client))
	return VerifyOTPLogin200JSONResponse{Body: body, Headers: VerifyOTPLogin200ResponseHeaders{SetCookie: cookie}}, nil
}

// StartPhoneVerification implements StrictServerInterface.
func (h *Handlers) StartPhoneVerification(ctx context.Context, req StartPhoneVerificationRequestObject) (StartPhoneVerificationResponseObject, error) {
	if req.Body == nil {
		return nil, errBodyRequired
	}
	ch, err := h.svc.StartPhoneVerification(ctx, req.Body.Phone, region(req.Body.Region))
	if err != nil {
		return nil, err
	}
	return StartPhoneVerification202JSONResponse{ChallengeId: ch.ID, ExpiresAt: ch.ExpiresAt}, nil
}

// VerifyPhone implements StrictServerInterface.
func (h *Handlers) VerifyPhone(ctx context.Context, req VerifyPhoneRequestObject) (VerifyPhoneResponseObject, error) {
	if req.Body == nil {
		return nil, errBodyRequired
	}
	if err := h.svc.VerifyPhone(ctx, req.Body.ChallengeId, req.Body.Code); err != nil {
		return nil, err
	}
	return VerifyPhone204Response{}, nil
}

// SetupTOTP implements StrictServerInterface.
func (h *Handlers) SetupTOTP(ctx context.Context, _ SetupTOTPRequestObject) (SetupTOTPResponseObject, error) {
	s, err := h.svc.SetupTOTP(ctx)
	if err != nil {
		return nil, err
	}
	return SetupTOTP200JSONResponse{Secret: s.Secret, OtpauthUri: s.URI}, nil
}

// ConfirmTOTP implements StrictServerInterface.
func (h *Handlers) ConfirmTOTP(ctx context.Context, req ConfirmTOTPRequestObject) (ConfirmTOTPResponseObject, error) {
	if req.Body == nil {
		return nil, errBodyRequired
	}
	codes, err := h.svc.ConfirmTOTP(ctx, req.Body.Code)
	if err != nil {
		return nil, err
	}
	return ConfirmTOTP200JSONResponse{Codes: codes}, nil
}

// DisableTOTP implements StrictServerInterface.
func (h *Handlers) DisableTOTP(ctx context.Context, req DisableTOTPRequestObject) (DisableTOTPResponseObject, error) {
	if req.Body == nil {
		return nil, errBodyRequired
	}
	if err := h.svc.DisableTOTP(ctx, req.Body.Password, req.Body.Code); err != nil {
		return nil, err
	}
	return DisableTOTP204Response{}, nil
}

// AcceptInvitation implements StrictServerInterface.
func (h *Handlers) AcceptInvitation(ctx context.Context, req AcceptInvitationRequestObject) (AcceptInvitationResponseObject, error) {
	if req.Body == nil {
		return nil, errBodyRequired
	}
	in := app.AcceptInput{Token: req.Body.Token}
	if req.Body.Name != nil {
		in.Name = *req.Body.Name
	}
	if req.Body.Password != nil {
		in.Password = *req.Body.Password
	}
	res, err := h.svc.AcceptInvitation(ctx, in)
	if err != nil {
		return nil, err
	}
	out := AcceptInvitation200JSONResponse{Body: AcceptInvitationResponse{
		StoreId: res.StoreID.String(), Role: Role(res.Role), User: toUser(res.User),
	}}
	if res.Auth != nil {
		body, cookie := authResponse(*res.Auth, mobile(req.Body.Client))
		out.Body.ExpiresAt = &body.ExpiresAt
		out.Body.Token = body.Token
		out.Headers.SetCookie = cookie
	}
	return out, nil
}

// ListMembers implements StrictServerInterface.
func (h *Handlers) ListMembers(ctx context.Context, _ ListMembersRequestObject) (ListMembersResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermMembersRead); err != nil {
		return nil, err
	}
	ms, err := h.svc.Members(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]Member, len(ms))
	for i, m := range ms {
		items[i] = Member{UserId: m.UserID.String(), Name: m.Name, Email: m.Email, Role: Role(m.Role), JoinedAt: m.JoinedAt}
	}
	return ListMembers200JSONResponse{Items: items}, nil
}

var errMemberNotFound = errs.NotFound("member_not_found", "member not found")

func pathID(s string, notFound error) (ulid.ID, error) {
	id, err := ulid.Parse(s)
	if err != nil {
		return ulid.ID{}, notFound
	}
	return id, nil
}

// UpdateMemberRole implements StrictServerInterface.
func (h *Handlers) UpdateMemberRole(ctx context.Context, req UpdateMemberRoleRequestObject) (UpdateMemberRoleResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermMembersManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	id, err := pathID(req.UserId, errMemberNotFound)
	if err != nil {
		return nil, err
	}
	if err := h.svc.SetMemberRole(ctx, id, string(req.Body.Role)); err != nil {
		return nil, err
	}
	return UpdateMemberRole204Response{}, nil
}

// RemoveMember implements StrictServerInterface.
func (h *Handlers) RemoveMember(ctx context.Context, req RemoveMemberRequestObject) (RemoveMemberResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermMembersManage); err != nil {
		return nil, err
	}
	id, err := pathID(req.UserId, errMemberNotFound)
	if err != nil {
		return nil, err
	}
	if err := h.svc.RemoveMember(ctx, id); err != nil {
		return nil, err
	}
	return RemoveMember204Response{}, nil
}

// ListInvitations implements StrictServerInterface.
func (h *Handlers) ListInvitations(ctx context.Context, _ ListInvitationsRequestObject) (ListInvitationsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermMembersManage); err != nil {
		return nil, err
	}
	invs, err := h.svc.Invitations(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]Invitation, len(invs))
	for i, v := range invs {
		by := v.InvitedBy.String()
		items[i] = Invitation{Id: v.ID.String(), Email: v.Email, Role: Role(v.Role), InvitedBy: &by, CreatedAt: v.CreatedAt, ExpiresAt: v.ExpiresAt}
	}
	return ListInvitations200JSONResponse{Items: items}, nil
}

// CreateInvitation implements StrictServerInterface.
func (h *Handlers) CreateInvitation(ctx context.Context, req CreateInvitationRequestObject) (CreateInvitationResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermMembersManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	v, err := h.svc.Invite(ctx, req.Body.Email, string(req.Body.Role))
	if err != nil {
		return nil, err
	}
	by := v.InvitedBy.String()
	return CreateInvitation201JSONResponse{Id: v.ID.String(), Email: v.Email, Role: Role(v.Role), InvitedBy: &by, CreatedAt: v.CreatedAt, ExpiresAt: v.ExpiresAt}, nil
}

// RevokeInvitation implements StrictServerInterface.
func (h *Handlers) RevokeInvitation(ctx context.Context, req RevokeInvitationRequestObject) (RevokeInvitationResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermMembersManage); err != nil {
		return nil, err
	}
	id, err := pathID(req.InvitationId, errs.NotFound("invitation_not_found", "invitation not found or expired"))
	if err != nil {
		return nil, err
	}
	if err := h.svc.RevokeInvitation(ctx, id); err != nil {
		return nil, err
	}
	return RevokeInvitation204Response{}, nil
}
