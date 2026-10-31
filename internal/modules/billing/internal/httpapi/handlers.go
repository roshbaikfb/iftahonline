// Package httpapi serves the billing tag of api/openapi.yaml. Handlers
// authorize first, take the store only from the context (set by the
// membership middleware, never from the path), and return errs errors
// that the problem package renders.
package httpapi

import (
	"context"
	"maps"
	"net/http"
	"slices"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var errBody = errs.BadRequest("request body is required", nil)

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
	s, err := h.summary(ctx)
	if err != nil {
		return nil, err
	}
	return GetBilling200JSONResponse(s), nil
}

// summary authorizes billing.manage and reads the store's billing.
func (h *Handlers) summary(ctx context.Context) (Billing, error) {
	if err := h.auth.Authorize(ctx, authz.PermBillingManage); err != nil {
		return Billing{}, err
	}
	s, err := h.svc.Summary(ctx)
	if err != nil {
		return Billing{}, err
	}
	return toBilling(s), nil
}

func toBilling(s app.Summary) Billing {
	out := Billing{PlanNameAr: s.PlanNameAr, PlanNameEn: s.PlanNameEn, Status: BillingStatus(s.Status),
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
	if s.NextPlanNameAr != "" {
		out.NextPlanNameAr, out.NextPlanNameEn = &s.NextPlanNameAr, &s.NextPlanNameEn
	}
	if s.NextCycle != "" {
		c := BillingNextCycle(s.NextCycle)
		out.NextCycle = &c
	}
	for i, inv := range s.OpenInvoices {
		o := OpenInvoice{Id: inv.ID.String(), Number: inv.Number, Period: openapi_types.Date{Time: inv.Period}, Currency: OpenInvoiceCurrency(inv.Currency),
			TotalMinor: inv.TotalMinor, DueAt: inv.DueAt, Attempts: inv.Attempts}
		if !inv.NextAttempt.IsZero() {
			o.NextAttemptAt = &s.OpenInvoices[i].NextAttempt
		}
		out.OpenInvoices[i] = o
	}
	return out
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

// ListBillingPlans implements StrictServerInterface.
func (h *Handlers) ListBillingPlans(ctx context.Context, _ ListBillingPlansRequestObject) (ListBillingPlansResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermBillingManage); err != nil {
		return nil, err
	}
	o, err := h.svc.Offers(ctx)
	if err != nil {
		return nil, err
	}
	out := ListBillingPlans200JSONResponse{Currency: BillingPlansCurrency(o.Currency), Features: make([]BillingFeature, len(o.Features)),
		Plans: make([]BillingPlan, len(o.Plans))}
	for i, f := range o.Features {
		out.Features[i] = BillingFeature{Key: f.Key, Kind: BillingFeatureKind(f.Kind), NameAr: f.NameAr, NameEn: f.NameEn}
	}
	for i, p := range o.Plans {
		bp := BillingPlan{Code: p.Code, Kind: BillingPlanKind(p.Kind), NameAr: p.NameAr, NameEn: p.NameEn, TrialDays: int(p.TrialDays),
			MonthlyMinor: p.Monthly, AnnualMinor: p.Annual, Current: p.Current, TrialAvailable: p.TrialAvailable,
			Grants: make([]BillingGrant, 0, len(p.Grants))}
		if p.Fee != nil {
			bp.Fee = &BillingPlanFee{RateBps: int(p.Fee.RateBps), MinMinor: p.Fee.MinMinor, MaxMinor: p.Fee.MaxMinor}
		}
		for _, key := range slices.Sorted(maps.Keys(p.Grants)) {
			g := p.Grants[key]
			bp.Grants = append(bp.Grants, BillingGrant{Feature: key, Unlimited: g.Unlimited, Value: g.Value})
		}
		out.Plans[i] = bp
	}
	return out, nil
}

// ChoosePlan implements StrictServerInterface.
func (h *Handlers) ChoosePlan(ctx context.Context, req ChoosePlanRequestObject) (ChoosePlanResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermBillingManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBody
	}
	var cycle domain.Cycle
	if req.Body.Cycle != nil {
		cycle = domain.Cycle(*req.Body.Cycle)
	}
	o, err := h.svc.Choose(ctx, req.Body.PlanCode, cycle, app.Payer{})
	if err != nil {
		return nil, err
	}
	out := ChoosePlan200JSONResponse{Action: ChoosePlanResultAction(o.Action)}
	if o.CheckoutURL != "" {
		out.CheckoutUrl = &o.CheckoutURL
	}
	return out, nil
}

// StartTrial implements StrictServerInterface.
func (h *Handlers) StartTrial(ctx context.Context, req StartTrialRequestObject) (StartTrialResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermBillingManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBody
	}
	if err := h.svc.StartTrial(ctx, req.Body.PlanCode); err != nil {
		return nil, err
	}
	s, err := h.summary(ctx)
	if err != nil {
		return nil, err
	}
	return StartTrial200JSONResponse(s), nil
}

// CancelRenewal implements StrictServerInterface.
func (h *Handlers) CancelRenewal(ctx context.Context, _ CancelRenewalRequestObject) (CancelRenewalResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermBillingManage); err != nil {
		return nil, err
	}
	if err := h.svc.CancelRenewal(ctx); err != nil {
		return nil, err
	}
	s, err := h.summary(ctx)
	if err != nil {
		return nil, err
	}
	return CancelRenewal200JSONResponse(s), nil
}

// ResumeRenewal implements StrictServerInterface.
func (h *Handlers) ResumeRenewal(ctx context.Context, _ ResumeRenewalRequestObject) (ResumeRenewalResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermBillingManage); err != nil {
		return nil, err
	}
	if err := h.svc.ResumeRenewal(ctx); err != nil {
		return nil, err
	}
	s, err := h.summary(ctx)
	if err != nil {
		return nil, err
	}
	return ResumeRenewal200JSONResponse(s), nil
}
