package httpapi

import (
	"context"
	"fmt"
	"io"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var errInvoiceNotFound = errs.NotFound("invoice_not_found", "no invoice with this ID")

// AdminBillingSummary implements StrictServerInterface.
func (h *Handlers) AdminBillingSummary(ctx context.Context, _ AdminBillingSummaryRequestObject) (AdminBillingSummaryResponseObject, error) {
	t, err := h.svc.BillingTotals(ctx)
	if err != nil {
		return nil, err
	}
	out := AdminBillingSummary200JSONResponse{Open: currencyTotals(t.Open), Overdue: currencyTotals(t.Overdue),
		Collected: currencyTotals(t.Collected), MonthStart: t.MonthStart}
	out.Failed.Reasons = make([]AdminFailedReason, len(t.Failed))
	for i, f := range t.Failed {
		out.Failed.Count += f.Count
		out.Failed.Reasons[i] = AdminFailedReason{Outcome: AdminFailedReasonOutcome(f.Outcome), Reason: f.Reason, Count: f.Count}
	}
	return out, nil
}

func currencyTotals(in []billing.CurrencyTotal) []AdminCurrencyTotal {
	out := make([]AdminCurrencyTotal, len(in))
	for i, c := range in {
		out[i] = AdminCurrencyTotal{Currency: AdminCurrencyTotalCurrency(c.Currency), Count: c.Count, TotalMinor: c.TotalMinor, TaxMinor: c.TaxMinor}
	}
	return out
}

// AdminListInvoices implements StrictServerInterface.
func (h *Handlers) AdminListInvoices(ctx context.Context, req AdminListInvoicesRequestObject) (AdminListInvoicesResponseObject, error) {
	p := req.Params
	q := invoiceQuery((*string)(p.Status), (*string)(p.Currency), p.Q)
	page, err := h.svc.Invoices(ctx, q, deref(p.Cursor), deref(p.Limit))
	if err != nil {
		return nil, err
	}
	out := AdminListInvoices200JSONResponse{Items: make([]AdminInvoice, len(page.Items))}
	for i, inv := range page.Items {
		out.Items[i] = toInvoice(inv)
	}
	if page.NextCursor != "" {
		out.NextCursor = &page.NextCursor
	}
	return out, nil
}

// AdminExportInvoices implements StrictServerInterface. The file streams
// through a pipe: ExportInvoices has already validated and audited, so
// later failures only cut the file short.
func (h *Handlers) AdminExportInvoices(ctx context.Context, req AdminExportInvoicesRequestObject) (AdminExportInvoicesResponseObject, error) {
	p := req.Params
	ex, err := h.svc.ExportInvoices(ctx, invoiceQuery((*string)(p.Status), (*string)(p.Currency), p.Q))
	if err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	go func() {
		_, werr := ex.WriteTo(pw)
		pw.CloseWithError(werr)
	}()
	name := fmt.Sprintf(`attachment; filename="invoices-%s.csv"`, time.Now().UTC().Format("2006-01-02"))
	return AdminExportInvoices200TextcsvResponse{Body: pr, Headers: AdminExportInvoices200ResponseHeaders{ContentDisposition: &name}}, nil
}

func invoiceQuery(status, currency, q *string) billing.InvoiceQuery {
	return billing.InvoiceQuery{Status: deref(status), Currency: deref(currency), Q: deref(q)}
}

// AdminGetInvoice implements StrictServerInterface.
func (h *Handlers) AdminGetInvoice(ctx context.Context, req AdminGetInvoiceRequestObject) (AdminGetInvoiceResponseObject, error) {
	id, err := ulid.Parse(req.InvoiceId)
	if err != nil {
		return nil, errInvoiceNotFound
	}
	d, err := h.svc.Invoice(ctx, id)
	if err != nil {
		return nil, err
	}
	return AdminGetInvoice200JSONResponse(toInvoiceDetail(d)), nil
}

// AdminSettleAccountInvoice implements StrictServerInterface.
func (h *Handlers) AdminSettleAccountInvoice(ctx context.Context, req AdminSettleAccountInvoiceRequestObject) (AdminSettleAccountInvoiceResponseObject, error) {
	id, err := ulid.Parse(req.InvoiceId)
	if err != nil {
		return nil, errInvoiceNotFound
	}
	if req.Body == nil {
		return nil, errBody
	}
	d, err := h.svc.SettleAccountInvoice(ctx, id, req.Body.Reference, req.Body.Reason)
	if err != nil {
		return nil, err
	}
	return AdminSettleAccountInvoice200JSONResponse(toInvoiceDetail(d)), nil
}

// AdminListPayments implements StrictServerInterface.
func (h *Handlers) AdminListPayments(ctx context.Context, req AdminListPaymentsRequestObject) (AdminListPaymentsResponseObject, error) {
	p := req.Params
	q := billing.PaymentQuery{Outcome: deref((*string)(p.Outcome)), Gateway: deref(p.Gateway)}
	page, err := h.svc.Payments(ctx, q, deref(p.Cursor), deref(p.Limit))
	if err != nil {
		return nil, err
	}
	out := AdminListPayments200JSONResponse{Items: make([]AdminPayment, len(page.Items))}
	for i, pay := range page.Items {
		out.Items[i] = toPayment(pay)
	}
	if page.NextCursor != "" {
		out.NextCursor = &page.NextCursor
	}
	return out, nil
}

func toInvoice(inv billing.ConsoleInvoice) AdminInvoice {
	out := AdminInvoice{Id: inv.ID.String(), Number: inv.Number, AccountId: inv.Account.String(), AccountKind: AdminInvoiceAccountKind(inv.AccountKind),
		AccountName: inv.AccountName, Period: openapi_types.Date{Time: inv.Period}, Currency: AdminInvoiceCurrency(inv.Currency),
		SubtotalMinor: inv.SubtotalMinor, TaxBps: inv.TaxBps, TaxMinor: inv.TaxMinor, TotalMinor: inv.TotalMinor,
		Status: AdminInvoiceStatus(inv.Status), Overdue: inv.Overdue, IssuedAt: inv.IssuedAt, DueAt: inv.DueAt, PaidAt: inv.PaidAt,
		Attempts: inv.Attempts, NextAttemptAt: inv.NextAttemptAt, StoreId: idString(inv.Store)}
	if inv.LastError != "" {
		e := inv.LastError
		out.LastError = &e
	}
	return out
}

func toInvoiceDetail(d billing.InvoiceDetail) AdminInvoiceDetail {
	out := AdminInvoiceDetail{Invoice: toInvoice(d.Invoice), Lines: make([]AdminInvoiceLine, len(d.Lines)), Payments: make([]AdminPayment, len(d.Payments))}
	for i, l := range d.Lines {
		out.Lines[i] = AdminInvoiceLine{Kind: AdminInvoiceLineKind(l.Kind), Description: l.Description, Quantity: l.Quantity,
			AmountMinor: l.AmountMinor, StoreId: idString(l.Store)}
	}
	for i, p := range d.Payments {
		out.Payments[i] = toPayment(p)
	}
	return out
}

func toPayment(p billing.ConsolePayment) AdminPayment {
	out := AdminPayment{Id: p.ID.String(), CreatedAt: p.CreatedAt, InvoiceId: p.Invoice.String(), InvoiceNumber: p.InvoiceNumber,
		AccountKind: AdminPaymentAccountKind(p.AccountKind), AccountName: p.AccountName, StoreId: idString(p.Store), Gateway: p.Gateway,
		Outcome: AdminPaymentOutcome(p.Outcome), AmountMinor: p.AmountMinor, Currency: AdminPaymentCurrency(p.Currency), RefundedAt: p.RefundedAt}
	if p.Reason != "" {
		r := p.Reason
		out.Reason = &r
	}
	if p.Reference != "" {
		r := p.Reference
		out.Reference = &r
	}
	return out
}

func idString(id *ulid.ID) *string {
	if id == nil {
		return nil
	}
	s := id.String()
	return &s
}

// deref is *p, or the zero value for none.
func deref[T any](p *T) T {
	var v T
	if p != nil {
		v = *p
	}
	return v
}
