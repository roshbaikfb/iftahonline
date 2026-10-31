// Package httpapi serves the entitlements tag of api/openapi.yaml.
// Handlers authorize first, take the store only from the context (set by
// the membership middleware, never from the path), and return errs
// errors that the problem package renders.
package httpapi

import (
	"context"
	"net/http"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/domain"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
)

// Handlers implements StrictServerInterface.
type Handlers struct {
	svc  *app.Service
	auth authz.Authorizer
}

var _ StrictServerInterface = (*Handlers)(nil)

// Mount registers the entitlements routes on mux behind scope, the
// membership middleware (ADR-0006).
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

// GetUsage implements StrictServerInterface.
func (h *Handlers) GetUsage(ctx context.Context, _ GetUsageRequestObject) (GetUsageResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermBillingManage); err != nil {
		return nil, err
	}
	ms, err := h.svc.Meters(ctx)
	if err != nil {
		return nil, err
	}
	adFree, err := h.svc.Allowed(ctx, domain.AdFree) // the dashboard's notice of ads (ADR-0062)
	if err != nil {
		return nil, err
	}
	out := GetUsage200JSONResponse{Items: make([]UsageMeter, len(ms)), AdFree: adFree}
	for i, m := range ms {
		u := UsageMeter{Feature: UsageMeterFeature(m.Feature), Kind: UsageMeterKind(m.Kind), Used: m.Used, Unlimited: m.Unlimited}
		if !m.Unlimited {
			limit := m.Limit
			u.Limit = &limit
		}
		out.Items[i] = u
	}
	return out, nil
}
