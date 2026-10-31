package httpapi

import (
	"context"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// AdminHealth implements StrictServerInterface.
func (h *Handlers) AdminHealth(ctx context.Context, _ AdminHealthRequestObject) (AdminHealthResponseObject, error) {
	r, err := h.svc.Health(ctx)
	if err != nil {
		return nil, err
	}
	out := AdminHealth200JSONResponse{CheckedAt: r.CheckedAt, Database: r.Database, Redis: r.Redis, Errors: make([]AdminHealthErrors, len(r.Errors))}
	for i, e := range r.Errors {
		out.Errors[i] = AdminHealthErrors(e)
	}
	if c := r.Stores; c != nil {
		out.Stores = &AdminStoreCounts{Active: c.Active, Held: c.Held, Suspended: c.Suspended, Closed: c.Closed, Total: c.Total}
	}
	if b := r.Billing; b != nil {
		out.Billing = &AdminBillingHealth{Trialing: b.Trialing, Active: b.Active, PastDue: b.PastDue, Suspended: b.Suspended,
			Cancelled: b.Cancelled, InvoicesOpen: b.InvoicesOpen, InvoicesOverdue: b.InvoicesOverdue}
	}
	if j := r.Jobs; j != nil {
		out.Jobs = &AdminJobHealth{Available: j.Available, Running: j.Running, Retryable: j.Retryable, Discarded24h: j.Discarded24h,
			OldestAvailableSeconds: j.OldestAvailableSeconds}
	}
	return out, nil
}

// AdminGetStoreBilling implements StrictServerInterface.
func (h *Handlers) AdminGetStoreBilling(ctx context.Context, req AdminGetStoreBillingRequestObject) (AdminGetStoreBillingResponseObject, error) {
	id, err := storeID(req.StoreId)
	if err != nil {
		return nil, err
	}
	s, err := h.svc.StoreBilling(ctx, id)
	if err != nil {
		return nil, err
	}
	return AdminGetStoreBilling200JSONResponse(toBilling(s)), nil
}

// AdminSettleInvoice implements StrictServerInterface.
func (h *Handlers) AdminSettleInvoice(ctx context.Context, req AdminSettleInvoiceRequestObject) (AdminSettleInvoiceResponseObject, error) {
	id, err := storeID(req.StoreId)
	if err != nil {
		return nil, err
	}
	invoice, err := ulid.Parse(req.InvoiceId)
	if err != nil {
		return nil, errs.NotFound("invoice_not_found", "invoice not found")
	}
	if req.Body == nil {
		return nil, errBody
	}
	s, err := h.svc.SettleInvoice(ctx, id, invoice, req.Body.Reference, req.Body.Reason)
	if err != nil {
		return nil, err
	}
	return AdminSettleInvoice200JSONResponse(toBilling(s)), nil
}

// toBilling is the merchant's billing page's shape (Billing).
func toBilling(s billing.Summary) Billing {
	out := Billing{PlanNameAr: s.PlanNameAr, PlanNameEn: s.PlanNameEn, Status: BillingStatus(s.Status), OpenInvoices: make([]OpenInvoice, len(s.OpenInvoices))}
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
	return out
}
