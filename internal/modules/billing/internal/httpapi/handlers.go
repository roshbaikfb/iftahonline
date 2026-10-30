// Package httpapi serves the billing tag of api/openapi.yaml. Handlers
// authorize first, take the store only from the context (set by the
// membership middleware, never from the path), and return errs errors
// that the problem package renders.
package httpapi

import (
	"context"
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
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

// Mount registers the billing routes on mux. scope is the membership
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

// GetBilling implements StrictServerInterface.
func (h *Handlers) GetBilling(ctx context.Context, _ GetBillingRequestObject) (GetBillingResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermBillingManage); err != nil {
		return nil, err
	}
	s, err := h.svc.Summary(ctx)
	if err != nil {
		return nil, err
	}
	out := GetBilling200JSONResponse{PlanNameAr: s.PlanNameAr, PlanNameEn: s.PlanNameEn, Status: BillingStatus(s.Status),
		OpenInvoices: make([]OpenInvoice, len(s.OpenInvoices))}
	if s.Cycle != "" {
		c := BillingCycle(s.Cycle)
		out.Cycle = &c
	}
	if !s.TrialEndsAt.IsZero() {
		out.TrialEndsAt = &s.TrialEndsAt
	}
	if !s.PeriodEnd.IsZero() {
		out.PeriodEnd = &s.PeriodEnd
	}
	if !s.GraceUntil.IsZero() {
		out.GraceUntil = &s.GraceUntil
	}
	if s.CancelAtPeriodEnd {
		out.CancelAtPeriodEnd = &s.CancelAtPeriodEnd
	}
	if s.Card != "" {
		out.Card = &s.Card
	}
	for i, inv := range s.OpenInvoices {
		o := OpenInvoice{Id: inv.ID.String(), Number: inv.Number, Period: openapi_types.Date{Time: inv.Period}, Currency: OpenInvoiceCurrency(inv.Currency),
			TotalMinor: inv.TotalMinor, DueAt: inv.DueAt, Attempts: inv.Attempts}
		if !inv.NextAttempt.IsZero() {
			o.NextAttemptAt = &s.OpenInvoices[i].NextAttempt
		}
		out.OpenInvoices[i] = o
	}
	return out, nil
}

// PayInvoice implements StrictServerInterface.
func (h *Handlers) PayInvoice(ctx context.Context, req PayInvoiceRequestObject) (PayInvoiceResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermBillingManage); err != nil {
		return nil, err
	}
	id, err := ulid.Parse(req.InvoiceId)
	if err != nil {
		return nil, app.ErrInvoiceNotFound
	}
	u, err := h.svc.PayNow(ctx, id, app.Payer{})
	if err != nil {
		return nil, err
	}
	return PayInvoice200JSONResponse{CheckoutUrl: u}, nil
}
