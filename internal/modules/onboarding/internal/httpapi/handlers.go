// Package httpapi serves the onboarding tag of api/openapi.yaml. Handlers
// authorize first, take the store only from the context (set by the
// membership middleware, never from the path), and return errs errors
// that the problem package renders.
package httpapi

import (
	"context"
	"net/http"

	"github.com/roshbaikfb/iftahonline/internal/modules/onboarding/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/onboarding/internal/domain"
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

// Mount registers the onboarding routes on mux. scope is the membership
// middleware: it authenticates, returns 404 to non-members, and sets the
// tenant and principal (ADR-0006).
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

// GetOnboarding implements StrictServerInterface.
func (h *Handlers) GetOnboarding(ctx context.Context, _ GetOnboardingRequestObject) (GetOnboardingResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermStoreRead); err != nil {
		return nil, err
	}
	st, err := h.svc.Get(ctx)
	if err != nil {
		return nil, err
	}
	return GetOnboarding200JSONResponse(toOnboarding(st)), nil
}

// ChooseOnboardingTemplate implements StrictServerInterface.
func (h *Handlers) ChooseOnboardingTemplate(ctx context.Context, req ChooseOnboardingTemplateRequestObject) (ChooseOnboardingTemplateResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermStoreUpdate); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errs.BadRequest("request body is required", nil)
	}
	st, err := h.svc.Choose(ctx, domain.Choice{
		Activity: domain.Activity(req.Body.Activity), Look: domain.Look(req.Body.Look), Color: req.Body.Color,
	})
	if err != nil {
		return nil, err
	}
	return ChooseOnboardingTemplate202JSONResponse(toOnboarding(st)), nil
}

// CompleteOnboarding implements StrictServerInterface.
func (h *Handlers) CompleteOnboarding(ctx context.Context, _ CompleteOnboardingRequestObject) (CompleteOnboardingResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermStoreUpdate); err != nil {
		return nil, err
	}
	st, err := h.svc.Complete(ctx)
	if err != nil {
		return nil, err
	}
	return CompleteOnboarding200JSONResponse(toOnboarding(st)), nil
}

func toOnboarding(st domain.State) Onboarding {
	out := Onboarding{Status: OnboardingStatus(st.Status()), CompletedAt: st.CompletedAt}
	out.Template.Status = OnboardingTemplateStatus(st.Template)
	if st.TemplateVersion > 0 {
		v := st.TemplateVersion
		out.Template.Version = &v
	}
	if c := st.Choice; c != nil {
		activity, look, color := OnboardingActivity(c.Activity), OnboardingLook(c.Look), c.Color
		out.Activity, out.Look, out.Color = &activity, &look, &color
	}
	return out
}
