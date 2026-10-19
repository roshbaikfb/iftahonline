// Package httpapi serves the admin tag of api/openapi.yaml: the
// super-admin console, on the admin host only.
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

// Handlers implements StrictServerInterface.
type Handlers struct{ svc *app.Service }

var _ StrictServerInterface = (*Handlers)(nil)

// Mount registers the console routes behind scope.
func Mount(mux *http.ServeMux, svc *app.Service, scope func(http.Handler) http.Handler, resp *problem.Responder) {
	badRequest := func(w http.ResponseWriter, r *http.Request, err error) {
		resp.Write(w, r, errs.BadRequest("invalid request", err))
	}
	strict := NewStrictHandlerWithOptions(&Handlers{svc: svc}, nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc: badRequest, ResponseErrorHandlerFunc: resp.Write,
	})
	HandlerWithOptions(strict, StdHTTPServerOptions{
		BaseRouter: mux, Middlewares: []MiddlewareFunc{MiddlewareFunc(scope)}, ErrorHandlerFunc: badRequest,
	})
}

var (
	errBody          = errs.BadRequest("request body is required", nil)
	errStoreNotFound = errs.NotFound("store_not_found", "store not found")
)

// CookieName is the console session cookie. SameSite=Strict: the console
// is never entered by following a link from another site.
const CookieName = "__Host-asid"

func sessionCookie(token string, maxAge int) string {
	return (&http.Cookie{Name: CookieName, Value: token, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode}).String()
}

func profile(a domain.Admin) AdminProfile {
	return AdminProfile{Id: a.ID.String(), Email: a.Email, Name: a.Name, LastLoginAt: a.LastLoginAt}
}

// AdminLogin implements StrictServerInterface.
func (h *Handlers) AdminLogin(ctx context.Context, req AdminLoginRequestObject) (AdminLoginResponseObject, error) {
	if req.Body == nil {
		return nil, errBody
	}
	res, err := h.svc.Login(ctx, req.Body.Email, req.Body.Password, req.Body.Code)
	if err != nil {
		return nil, err
	}
	cookie := sessionCookie(string(res.Token), int(domain.SessionMaxLifetime.Seconds()))
	return AdminLogin200JSONResponse{
		Body:    AdminAuth{Admin: profile(res.Admin), ExpiresAt: res.Expires},
		Headers: AdminLogin200ResponseHeaders{SetCookie: &cookie},
	}, nil
}

// AdminLogout implements StrictServerInterface.
func (h *Handlers) AdminLogout(ctx context.Context, _ AdminLogoutRequestObject) (AdminLogoutResponseObject, error) {
	if err := h.svc.Logout(ctx); err != nil {
		return nil, err
	}
	cleared := sessionCookie("", -1)
	return AdminLogout204Response{Headers: AdminLogout204ResponseHeaders{SetCookie: &cleared}}, nil
}

// AdminMe implements StrictServerInterface.
func (h *Handlers) AdminMe(ctx context.Context, _ AdminMeRequestObject) (AdminMeResponseObject, error) {
	a, err := h.svc.Me(ctx)
	if err != nil {
		return nil, err
	}
	return AdminMe200JSONResponse(profile(a)), nil
}

// AdminListStores implements StrictServerInterface.
func (h *Handlers) AdminListStores(ctx context.Context, req AdminListStoresRequestObject) (AdminListStoresResponseObject, error) {
	p := req.Params
	var query, status, cursor string
	limit := 0
	if p.Query != nil {
		query = *p.Query
	}
	if p.Status != nil {
		status = string(*p.Status)
	}
	if p.Cursor != nil {
		cursor = *p.Cursor
	}
	if p.Limit != nil {
		limit = *p.Limit
	}
	page, err := h.svc.Stores(ctx, query, status, cursor, limit)
	if err != nil {
		return nil, err
	}
	out := AdminListStores200JSONResponse{Items: make([]AdminStoreSummary, len(page.Items))}
	for i, e := range page.Items {
		out.Items[i] = AdminStoreSummary{Store: toStore(e.Store), PrimaryHost: e.PrimaryHost}
	}
	if page.NextCursor != "" {
		out.NextCursor = &page.NextCursor
	}
	return out, nil
}

func storeID(raw string) (tenant.StoreID, error) {
	id, err := tenant.ParseStoreID(raw)
	if err != nil {
		return tenant.StoreID{}, errStoreNotFound
	}
	return id, nil
}

// AdminGetStore implements StrictServerInterface.
func (h *Handlers) AdminGetStore(ctx context.Context, req AdminGetStoreRequestObject) (AdminGetStoreResponseObject, error) {
	id, err := storeID(req.StoreId)
	if err != nil {
		return nil, err
	}
	st, ds, err := h.svc.Store(ctx, id)
	if err != nil {
		return nil, err
	}
	out := AdminGetStore200JSONResponse{Store: toStore(st), Domains: make([]Domain, len(ds))}
	for i, d := range ds {
		out.Domains[i] = Domain{Id: d.ID.String(), Host: d.Host, Kind: DomainKind(d.Kind), Status: DomainStatus(d.Status),
			IsPrimary: d.IsPrimary, VerifiedAt: d.VerifiedAt, LastCheckedAt: d.LastCheckedAt, CreatedAt: d.CreatedAt}
	}
	return out, nil
}

// AdminSuspendStore implements StrictServerInterface.
func (h *Handlers) AdminSuspendStore(ctx context.Context, req AdminSuspendStoreRequestObject) (AdminSuspendStoreResponseObject, error) {
	id, err := storeID(req.StoreId)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBody
	}
	st, err := h.svc.Suspend(ctx, id, req.Body.Reason)
	if err != nil {
		return nil, err
	}
	return AdminSuspendStore200JSONResponse(toStore(st)), nil
}

// AdminReactivateStore implements StrictServerInterface.
func (h *Handlers) AdminReactivateStore(ctx context.Context, req AdminReactivateStoreRequestObject) (AdminReactivateStoreResponseObject, error) {
	id, err := storeID(req.StoreId)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBody
	}
	st, err := h.svc.Reactivate(ctx, id, req.Body.Reason)
	if err != nil {
		return nil, err
	}
	return AdminReactivateStore200JSONResponse(toStore(st)), nil
}

// AdminAuditLog implements StrictServerInterface.
func (h *Handlers) AdminAuditLog(ctx context.Context, req AdminAuditLogRequestObject) (AdminAuditLogResponseObject, error) {
	var cursor string
	limit := 0
	if req.Params.Cursor != nil {
		cursor = *req.Params.Cursor
	}
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	page, err := h.svc.Audit(ctx, cursor, limit)
	if err != nil {
		return nil, err
	}
	out := AdminAuditLog200JSONResponse{Items: make([]AdminAuditEntry, len(page.Items))}
	for i, e := range page.Items {
		item := AdminAuditEntry{Id: e.ID.String(), OccurredAt: e.OccurredAt, Action: e.Action, TargetType: e.TargetType,
			TargetId: e.TargetID, IpPrefix: e.IPPrefix, Metadata: map[string]any{}}
		if e.AdminID != nil {
			id := e.AdminID.String()
			item.AdminId = &id
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

func toStore(s tenancy.Store) Store {
	return Store{Id: s.ID.String(), Slug: s.Slug, Name: s.Name, Status: StoreStatus(s.Status), Plan: StorePlan(s.Plan),
		Currency: StoreCurrency(s.Currency), Locale: StoreLocale(s.Locale), Timezone: s.Timezone,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt}
}
