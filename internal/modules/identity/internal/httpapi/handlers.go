// Package httpapi serves the identity tag of api/openapi.yaml and provides
// the authentication/membership middleware every API route runs behind.
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Handlers implements StrictServerInterface.
type Handlers struct {
	svc  *app.Service
	auth authz.Authorizer
}

var _ StrictServerInterface = (*Handlers)(nil)

// Mount registers the identity routes behind scope (see Scope).
func Mount(mux *http.ServeMux, svc *app.Service, auth authz.Authorizer, scope func(http.Handler) http.Handler, resp *problem.Responder) {
	badRequest := func(w http.ResponseWriter, r *http.Request, err error) {
		resp.Write(w, r, errs.BadRequest("invalid request", err))
	}
	strict := NewStrictHandlerWithOptions(&Handlers{svc: svc, auth: auth}, nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  badRequest,
		ResponseErrorHandlerFunc: resp.Write,
	})
	HandlerWithOptions(strict, StdHTTPServerOptions{
		BaseRouter:       mux,
		Middlewares:      []MiddlewareFunc{MiddlewareFunc(scope)},
		ErrorHandlerFunc: badRequest,
	})
}

func mobile(c *ClientKind) bool { return c != nil && *c == Mobile }

func authResponse(res app.AuthResult, isMobile bool) (AuthResponse, *string) {
	body := AuthResponse{User: toUser(res.User), ExpiresAt: res.Session.ExpiresAt}
	if !res.StoreID.IsZero() {
		id := res.StoreID.String()
		body.StoreId = &id
	}
	if isMobile {
		tok := string(res.Token)
		body.Token = &tok
		return body, nil
	}
	cookie := sessionCookie(string(res.Token), int(domain.SessionMaxLifetime.Seconds()))
	return body, &cookie
}

// Signup implements StrictServerInterface.
func (h *Handlers) Signup(ctx context.Context, req SignupRequestObject) (SignupResponseObject, error) {
	if req.Body == nil {
		return nil, errs.BadRequest("request body is required", nil)
	}
	b := req.Body
	res, err := h.svc.Signup(ctx, app.SignupInput{
		Email: b.Email, Password: b.Password, Name: b.Name,
		Store: tenancy.NewStore{Slug: b.Store.Slug, Name: b.Store.Name, Currency: string(b.Store.Currency),
			Locale: string(b.Store.Locale), Timezone: b.Store.Timezone},
	})
	if err != nil {
		return nil, err
	}
	body, cookie := authResponse(res, mobile(b.Client))
	return Signup201JSONResponse{Body: body, Headers: Signup201ResponseHeaders{SetCookie: cookie}}, nil
}

// CheckSlugAvailability implements StrictServerInterface.
func (h *Handlers) CheckSlugAvailability(ctx context.Context, req CheckSlugAvailabilityRequestObject) (CheckSlugAvailabilityResponseObject, error) {
	a, err := h.svc.CheckSlug(ctx, req.Params.Slug)
	if err != nil {
		return nil, err
	}
	out := CheckSlugAvailability200JSONResponse{Slug: a.Slug, Available: a.Available}
	if a.Reason != "" {
		r := SlugAvailabilityReason(a.Reason)
		out.Reason = &r
	}
	return out, nil
}

// Login implements StrictServerInterface.
func (h *Handlers) Login(ctx context.Context, req LoginRequestObject) (LoginResponseObject, error) {
	if req.Body == nil {
		return nil, errs.BadRequest("request body is required", nil)
	}
	res, err := h.svc.Login(ctx, req.Body.Email, req.Body.Password)
	if err != nil {
		return nil, err
	}
	if res.MFA != nil {
		return Login202JSONResponse(mfaRequired(res.MFA)), nil
	}
	body, cookie := authResponse(res, mobile(req.Body.Client))
	return Login200JSONResponse{Body: body, Headers: Login200ResponseHeaders{SetCookie: cookie}}, nil
}

// Logout implements StrictServerInterface.
func (h *Handlers) Logout(ctx context.Context, _ LogoutRequestObject) (LogoutResponseObject, error) {
	if err := h.svc.Logout(ctx); err != nil {
		return nil, err
	}
	cleared := sessionCookie("", -1)
	return Logout204Response{Headers: Logout204ResponseHeaders{SetCookie: &cleared}}, nil
}

// GetMe implements StrictServerInterface.
func (h *Handlers) GetMe(ctx context.Context, _ GetMeRequestObject) (GetMeResponseObject, error) {
	u, ms, err := h.svc.Me(ctx)
	if err != nil {
		return nil, err
	}
	out := GetMe200JSONResponse{User: toUser(u), Memberships: make([]Membership, len(ms))}
	for i, m := range ms {
		out.Memberships[i] = Membership{StoreId: m.StoreID.String(), Role: Role(m.Role)}
	}
	return out, nil
}

// ListMySessions implements StrictServerInterface.
func (h *Handlers) ListMySessions(ctx context.Context, _ ListMySessionsRequestObject) (ListMySessionsResponseObject, error) {
	ss, current, err := h.svc.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]SessionInfo, len(ss))
	for i, s := range ss {
		items[i] = SessionInfo{Id: s.ID.String(), UserAgent: s.UserAgent, IpPrefix: s.IPPrefix,
			CreatedAt: s.CreatedAt, LastSeenAt: s.LastSeenAt, ExpiresAt: s.ExpiresAt, Current: s.ID == current}
	}
	return ListMySessions200JSONResponse{Items: items}, nil
}

// RevokeMySession implements StrictServerInterface.
func (h *Handlers) RevokeMySession(ctx context.Context, req RevokeMySessionRequestObject) (RevokeMySessionResponseObject, error) {
	id, err := ulid.Parse(req.SessionId)
	if err != nil {
		return nil, errs.NotFound("session_not_found", "session not found")
	}
	if err := h.svc.RevokeSession(ctx, id); err != nil {
		return nil, err
	}
	return RevokeMySession204Response{}, nil
}

// ListAuditLog implements StrictServerInterface.
func (h *Handlers) ListAuditLog(ctx context.Context, req ListAuditLogRequestObject) (ListAuditLogResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermAuditRead); err != nil {
		return nil, err
	}
	var cursor string
	if req.Params.Cursor != nil {
		cursor = *req.Params.Cursor
	}
	limit := 0
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	page, err := h.svc.ListAudit(ctx, cursor, limit)
	if err != nil {
		return nil, err
	}
	out := ListAuditLog200JSONResponse{Items: make([]AuditEntry, len(page.Items))}
	for i, e := range page.Items {
		item := AuditEntry{Id: e.ID.String(), OccurredAt: e.OccurredAt, Action: e.Action, TargetType: e.TargetType,
			TargetId: e.TargetID, IpPrefix: e.IPPrefix, Metadata: map[string]any{}}
		if e.ActorUserID != nil {
			actor := e.ActorUserID.String()
			item.ActorUserId = &actor
		}
		if len(e.Metadata) > 0 {
			if err := json.Unmarshal(e.Metadata, &item.Metadata); err != nil {
				return nil, err
			}
		}
		out.Items[i] = item
	}
	if page.NextCursor != "" {
		out.NextCursor = &page.NextCursor
	}
	return out, nil
}

func toUser(u domain.User) User {
	out := User{Id: u.ID.String(), Email: u.Email, Name: u.Name, EmailVerified: u.EmailVerifiedAt != nil,
		MfaEnabled: u.MFAEnabled(), CreatedAt: u.CreatedAt}
	if u.Phone != "" {
		out.Phone = &u.Phone
	}
	return out
}

func mfaRequired(c *app.Challenge) MFARequired {
	return MFARequired{MfaRequired: true, ChallengeId: c.ID, ExpiresAt: c.ExpiresAt}
}

// CookieName is the dashboard session cookie. The __Host- prefix makes
// browsers require Secure, Path=/ and no Domain, so no subdomain (a
// merchant's storefront included) can set or read it.
const CookieName = "__Host-sid"

func sessionCookie(token string, maxAge int) string {
	return (&http.Cookie{
		Name: CookieName, Value: token, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	}).String()
}
