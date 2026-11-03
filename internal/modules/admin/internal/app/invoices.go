package app

import (
	"context"
	"encoding/csv"
	"io"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// The console's invoices and payments page: every account's, store and
// agency alike. A store's account shows the store's name now, through
// the directory (one more query a page).

// billingConsole is the wired billing for a signed-in admin.
func (s *Service) billingConsole(ctx context.Context) (Identity, billing.Console, error) {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return Identity{}, nil, errNoSession
	}
	if s.d.Billing == nil {
		return Identity{}, nil, errNoBilling
	}
	return who, s.d.Billing, nil
}

// BillingTotals is the page's summary.
func (s *Service) BillingTotals(ctx context.Context) (billing.Totals, error) {
	_, b, err := s.billingConsole(ctx)
	if err != nil {
		return billing.Totals{}, err
	}
	return b.Totals(ctx)
}

// Invoices lists every account's invoices through q, newest first.
func (s *Service) Invoices(ctx context.Context, q billing.InvoiceQuery, after string, limit int) (billing.InvoicePage, error) {
	_, b, err := s.billingConsole(ctx)
	if err != nil {
		return billing.InvoicePage{}, err
	}
	p, err := b.Invoices(ctx, q, after, limit)
	if err != nil {
		return billing.InvoicePage{}, err
	}
	return p, s.nameInvoices(ctx, p.Items)
}

// Invoice is one invoice with its lines and payment attempts.
func (s *Service) Invoice(ctx context.Context, id ulid.ID) (billing.InvoiceDetail, error) {
	_, b, err := s.billingConsole(ctx)
	if err != nil {
		return billing.InvoiceDetail{}, err
	}
	return s.invoice(ctx, b, id)
}

func (s *Service) invoice(ctx context.Context, b billing.Console, id ulid.ID) (billing.InvoiceDetail, error) {
	d, err := b.Invoice(ctx, id)
	if err != nil {
		return billing.InvoiceDetail{}, err
	}
	items := []billing.ConsoleInvoice{d.Invoice}
	if err = s.nameInvoices(ctx, items); err != nil {
		return billing.InvoiceDetail{}, err
	}
	d.Invoice = items[0]
	for i := range d.Payments {
		d.Payments[i].AccountName = d.Invoice.AccountName
	}
	return d, nil
}

// Payments lists every account's payment attempts through q, newest
// first.
func (s *Service) Payments(ctx context.Context, q billing.PaymentQuery, after string, limit int) (billing.PaymentPage, error) {
	_, b, err := s.billingConsole(ctx)
	if err != nil {
		return billing.PaymentPage{}, err
	}
	p, err := b.Payments(ctx, q, after, limit)
	if err != nil {
		return billing.PaymentPage{}, err
	}
	names, err := s.namesOf(ctx, len(p.Items), func(i int) (string, *ulid.ID) { return p.Items[i].AccountKind, p.Items[i].Store })
	if err != nil {
		return billing.PaymentPage{}, err
	}
	for i, it := range p.Items {
		if n, ok := names[storeKey(it.AccountKind, it.Store)]; ok {
			p.Items[i].AccountName = n
		}
	}
	return p, nil
}

// SettleAccountInvoice records any account's open invoice as paid by a transfer
// the platform confirmed, recorded first in the admin log with the
// reason, the reference and whose it is.
func (s *Service) SettleAccountInvoice(ctx context.Context, id ulid.ID, rawReference, rawReason string) (billing.InvoiceDetail, error) {
	who, b, err := s.billingConsole(ctx)
	if err != nil {
		return billing.InvoiceDetail{}, err
	}
	reason, err := domain.ValidateReason(rawReason)
	if err != nil {
		return billing.InvoiceDetail{}, err
	}
	reference := strings.TrimSpace(rawReference)
	if reference == "" || len(reference) > 120 {
		return billing.InvoiceDetail{}, errReference
	}
	d, err := b.Invoice(ctx, id)
	if err != nil {
		return billing.InvoiceDetail{}, err
	}
	inv := d.Invoice
	meta := map[string]any{"reason": reason, "reference": reference, "account": inv.Account.String(), "number": inv.Number}
	if inv.AccountKind == "store" && inv.Store != nil {
		meta["store"] = inv.Store.String()
	}
	if err = s.audit(ctx, &who.AdminID, "invoice.settled", "invoice", id.String(), meta); err != nil {
		return billing.InvoiceDetail{}, err
	}
	if err = b.SettleInvoice(ctx, inv.Account, id, reference); err != nil {
		return billing.InvoiceDetail{}, err
	}
	return s.invoice(ctx, b, id)
}

// storeKey keys a store account's name by its store; an agency's has
// none.
func storeKey(kind string, store *ulid.ID) ulid.ID {
	if kind != "store" || store == nil {
		return ulid.ID{}
	}
	return *store
}

// namesOf reads the current names of the n rows' stores (row i's kind
// and store).
func (s *Service) namesOf(ctx context.Context, n int, row func(i int) (string, *ulid.ID)) (map[ulid.ID]string, error) {
	var ids []ulid.ID
	seen := map[ulid.ID]bool{}
	for i := range n {
		if id := storeKey(row(i)); !id.IsZero() && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 || s.d.Directory == nil {
		return map[ulid.ID]string{}, nil
	}
	return s.d.Directory.Names(ctx, ids)
}

// nameInvoices gives store accounts' invoices their store's name now.
func (s *Service) nameInvoices(ctx context.Context, items []billing.ConsoleInvoice) error {
	names, err := s.namesOf(ctx, len(items), func(i int) (string, *ulid.ID) { return items[i].AccountKind, items[i].Store })
	if err != nil {
		return err
	}
	for i, it := range items {
		if n, ok := names[storeKey(it.AccountKind, it.Store)]; ok {
			items[i].AccountName = n
		}
	}
	return nil
}

// MaxInvoiceExport is the most rows an invoices export holds.
const MaxInvoiceExport = 50_000

// InvoiceExport writes one export of the invoices. ExportInvoices
// prepares it, so that a bad filter is reported before any of the file
// is sent.
type InvoiceExport struct {
	svc *Service
	ctx context.Context
	b   billing.Console
	q   billing.InvoiceQuery
	loc *time.Location
}

// ExportInvoices validates q (one page read) and records the export in
// the admin log with its filters; WriteTo then writes the file.
func (s *Service) ExportInvoices(ctx context.Context, q billing.InvoiceQuery) (*InvoiceExport, error) {
	who, b, err := s.billingConsole(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = b.Invoices(ctx, q, "", 1); err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation("Africa/Cairo")
	if err != nil {
		return nil, err
	}
	if err = s.audit(ctx, &who.AdminID, "billing.invoices_exported", "invoice", "",
		map[string]any{"status": q.Status, "currency": q.Currency, "q": q.Q}); err != nil {
		return nil, err
	}
	return &InvoiceExport{svc: s, ctx: ctx, b: b, q: q, loc: loc}, nil
}

// invoiceHeader is the CSV's first row; new columns go at the end.
func invoiceHeader() []string {
	return []string{"number", "issued_at", "account_kind", "account", "period", "currency", "subtotal", "vat_rate", "vat", "total",
		"status", "due_at", "paid_at"}
}

// WriteTo writes the invoices (newest first, at most MaxInvoiceExport)
// to w as CSV, page by page. Times are Cairo's; amounts are plain
// decimals. A UTF-8 BOM leads so spreadsheet apps read the Arabic names.
// It returns the number of invoices written.
func (e *InvoiceExport) WriteTo(w io.Writer) (int64, error) {
	if _, err := io.WriteString(w, "\ufeff"); err != nil {
		return 0, err
	}
	cw := csv.NewWriter(w)
	if err := cw.Write(invoiceHeader()); err != nil {
		return 0, err
	}
	rows, after := 0, ""
	for rows < MaxInvoiceExport {
		p, err := e.svc.Invoices(e.ctx, e.q, after, min(billing.MaxListPage, MaxInvoiceExport-rows))
		if err != nil {
			return int64(rows), err
		}
		for _, inv := range p.Items {
			if err = cw.Write(e.row(inv)); err != nil {
				return int64(rows), err
			}
		}
		rows += len(p.Items)
		cw.Flush()
		if err = cw.Error(); err != nil {
			return int64(rows), err
		}
		if p.NextCursor == "" {
			break
		}
		after = p.NextCursor
	}
	return int64(rows), nil
}

const exportTime = "2006-01-02 15:04"

func (e *InvoiceExport) row(inv billing.ConsoleInvoice) []string {
	units := money.Currency(inv.Currency).MinorUnits()
	status := inv.Status
	if inv.Overdue {
		status = "overdue"
	}
	paid := ""
	if inv.PaidAt != nil {
		paid = inv.PaidAt.In(e.loc).Format(exportTime)
	}
	return []string{inv.Number, inv.IssuedAt.In(e.loc).Format(exportTime), inv.AccountKind, csvCell(inv.AccountName),
		inv.Period.Format("2006-01"), inv.Currency, money.Decimal(inv.SubtotalMinor, units), money.Decimal(int64(inv.TaxBps), 2) + "%",
		money.Decimal(inv.TaxMinor, units), money.Decimal(inv.TotalMinor, units), status, inv.DueAt.In(e.loc).Format(exportTime), paid}
}

// csvCell neutralizes spreadsheet formulas (CSV injection): a name that
// a spreadsheet would evaluate gets a leading apostrophe.
func csvCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}
