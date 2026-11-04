package app

import (
	"cmp"
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/cursor"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// What the console reads and does about billing (ADR-0058).

// Health is the platform's subscriptions and invoices, counted.
type Health = store.Health

// OpenInvoice is an open invoice of an account.
type OpenInvoice = store.OpenInvoiceRow

var errInvoiceNotOpen = errs.NotFound("invoice_not_found", "no open invoice with this ID for the store's account")

// Health counts every store's subscription by status and the open and
// overdue invoices.
func (s *Service) Health(ctx context.Context) (h Health, err error) {
	err = postgres.InTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		h, txErr = store.PlatformHealth(ctx, tx, s.now())
		return txErr
	})
	return h, err
}

// Revenue is the monthly recurring revenue in one currency.
type Revenue = domain.Revenue

// Revenue is the platform's monthly recurring revenue per currency
// (ADR-0075): what every active subscription's next renewal is charged,
// as a month's share.
func (s *Service) Revenue(ctx context.Context) (r []Revenue, err error) {
	err = postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		paying, txErr := store.Paying(ctx, tx)
		r = domain.MRR(paying)
		return txErr
	})
	return r, err
}

// SettleStoreInvoice records invoice, an open invoice of the store-in-
// ctx's account, as paid outside the card on file (a transfer the
// platform confirmed, under reference): the account's stores settle as
// on any payment (ADR-0051). Any other invoice is 404.
func (s *Service) SettleStoreInvoice(ctx context.Context, invoice ulid.ID, reference string) error {
	var account ulid.ID
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		id, ok, err := accountOf(ctx, tx)
		if err != nil {
			return err
		}
		if !ok {
			return errInvoiceNotOpen
		}
		if err = store.UseAccount(ctx, tx, id); err != nil {
			return err
		}
		_, open, err := store.LockOpenInvoice(ctx, tx, invoice)
		if err == nil && !open {
			err = errInvoiceNotOpen
		}
		if err != nil {
			return err
		}
		account = id
		return nil
	})
	if err != nil {
		return err
	}
	return s.SettleInvoice(ctx, account, invoice, reference)
}

// DuplicatePayment is a payment of an invoice no longer open, to refund
// by hand (ADR-0063).
type DuplicatePayment = store.DuplicatePayment

var (
	errNoDuplicate     = errs.NotFound("duplicate_not_found", "no duplicate payment to refund with this ID")
	errRefundReference = errs.Validation(errs.FieldError{Field: "reference", Code: "invalid", Message: "1 to 120 characters"})
)

// DuplicatePayments lists every account's duplicate payments, those to
// refund first.
func (s *Service) DuplicatePayments(ctx context.Context) (out []DuplicatePayment, err error) {
	err = postgres.InTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		out, txErr = store.DuplicatePayments(ctx, tx)
		return txErr
	})
	return out, err
}

// MarkRefunded records duplicate payment id refunded on the gateway under
// reference; one already refunded, or no such duplicate, is 404.
func (s *Service) MarkRefunded(ctx context.Context, id ulid.ID, reference string) error {
	reference = strings.TrimSpace(reference)
	if reference == "" || len(reference) > 120 {
		return errRefundReference
	}
	return postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		account, ok, err := store.DuplicateAccount(ctx, tx, id)
		if err != nil {
			return err
		}
		if !ok {
			return errNoDuplicate
		}
		if err = store.UseAccount(ctx, tx, account); err != nil {
			return err
		}
		done, err := store.MarkRefunded(ctx, tx, id, reference, s.now().UTC())
		if err == nil && !done {
			err = errNoDuplicate
		}
		return err
	})
}

// ConsoleInvoice is an invoice as the console lists it; Overdue is open
// and past due.
type ConsoleInvoice struct {
	store.ConsoleInvoice
	Overdue bool
}

// InvoiceLine is a line of an invoice.
type InvoiceLine = store.InvoiceLine

// ConsolePayment is a payment attempt as the console lists it.
type ConsolePayment = store.ConsolePayment

// InvoiceQuery narrows the console's invoices; the zero query lists
// every one. Status is open, overdue, paid or void; Q an invoice number's
// start or a part of the account's name.
type InvoiceQuery struct {
	Status, Currency, Q string
}

// InvoicePage is a page of invoices.
type InvoicePage struct {
	Items      []ConsoleInvoice
	NextCursor string
}

// InvoiceDetail is one invoice with its lines and its payment attempts,
// newest first.
type InvoiceDetail struct {
	Invoice  ConsoleInvoice
	Lines    []InvoiceLine
	Payments []ConsolePayment
}

// PaymentQuery narrows the console's payments; the zero query lists every
// one.
type PaymentQuery struct {
	Outcome, Gateway string
}

// PaymentPage is a page of payment attempts.
type PaymentPage struct {
	Items      []ConsolePayment
	NextCursor string
}

// CurrencyTotal counts invoices in one currency: their total (VAT
// included) and the VAT in it.
type CurrencyTotal struct {
	Currency             string
	Count                int64
	TotalMinor, TaxMinor int64
}

// FailedCount counts failed payments of one outcome and reason.
type FailedCount struct {
	Outcome, Reason string
	Count           int64
}

// Totals is the console's billing summary: open and overdue invoices,
// those collected since MonthStart (the Cairo month's first moment), and
// the last 7 days' failed payments, most frequent first.
type Totals struct {
	Open, Overdue, Collected []CurrencyTotal
	MonthStart               time.Time
	Failed                   []FailedCount
}

// MaxListPage is the most rows a page of invoices or payments holds.
const MaxListPage = 500

var (
	errInvoiceNotFound = errs.NotFound("invoice_not_found", "no invoice with this ID")
	invoiceStatuses    = map[string]bool{"": true, "open": true, "overdue": true, "paid": true, "void": true} //nolint:gochecknoglobals // a fixed set
	currencies         = map[string]bool{"": true, "EGP": true, "SAR": true, "AED": true}                     //nolint:gochecknoglobals // a fixed set
	outcomes           = map[string]bool{"": true, "approved": true, "declined": true, "no_method": true,     //nolint:gochecknoglobals // a fixed set
		"manual": true, "duplicate": true}
	gatewayName = regexp.MustCompile(`^[a-z_]{0,32}$`)
)

// escapeLike makes s match itself in a LIKE pattern.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func badField(field, msg string) error {
	return errs.Validation(errs.FieldError{Field: field, Code: "invalid", Message: msg})
}

// page reads a page's size and the cursor it carries on from.
func page(after string, limit int) (int, *time.Time, *ulid.ID, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > MaxListPage {
		return 0, nil, nil, errs.Validation(errs.FieldError{Field: "limit", Code: "too_large", Message: "limit must be at most 500"})
	}
	if after == "" {
		return limit, nil, nil, nil
	}
	at, id, err := cursor.Decode(after)
	if err != nil {
		return 0, nil, nil, badField("cursor", "invalid cursor")
	}
	return limit, &at, &id, nil
}

// Invoices lists every account's invoices through q, newest first.
func (s *Service) Invoices(ctx context.Context, q InvoiceQuery, after string, limit int) (InvoicePage, error) {
	if !invoiceStatuses[q.Status] {
		return InvoicePage{}, badField("status", "open, overdue, paid or void")
	}
	if !currencies[q.Currency] {
		return InvoicePage{}, badField("currency", "EGP, SAR or AED")
	}
	text := strings.TrimSpace(q.Q)
	if utf8.RuneCountInString(text) > 100 {
		return InvoicePage{}, badField("q", "at most 100 characters")
	}
	limit, at, id, err := page(after, limit)
	if err != nil {
		return InvoicePage{}, err
	}
	now := s.now()
	f := store.InvoiceFilter{Status: q.Status, Currency: q.Currency, Q: escapeLike(text), Now: now, BeforeAt: at, BeforeID: id,
		Limit: limit + 1}
	var rows []store.ConsoleInvoice
	if err = postgres.InTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		rows, txErr = store.ConsoleInvoices(ctx, tx, f)
		return txErr
	}); err != nil {
		return InvoicePage{}, err
	}
	out := InvoicePage{}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		out.NextCursor = cursor.Encode(last.IssuedAt, last.ID)
	}
	out.Items = make([]ConsoleInvoice, len(rows))
	for i, r := range rows {
		out.Items[i] = consoleInvoice(r, now)
	}
	return out, nil
}

func consoleInvoice(r store.ConsoleInvoice, now time.Time) ConsoleInvoice {
	return ConsoleInvoice{ConsoleInvoice: r, Overdue: r.Status == "open" && r.DueAt.Before(now)}
}

// Invoice is any account's invoice id with its lines (read behind its
// account's wall) and its payment attempts, in one transaction.
func (s *Service) Invoice(ctx context.Context, id ulid.ID) (d InvoiceDetail, err error) {
	now := s.now()
	err = postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		rows, txErr := store.ConsoleInvoices(ctx, tx, store.InvoiceFilter{ID: &id, Now: now, Limit: 1})
		if txErr != nil {
			return txErr
		}
		if len(rows) == 0 {
			return errInvoiceNotFound
		}
		d.Invoice = consoleInvoice(rows[0], now)
		if txErr = store.UseAccount(ctx, tx, rows[0].Account); txErr != nil {
			return txErr
		}
		if d.Lines, txErr = store.InvoiceLines(ctx, tx, id); txErr != nil {
			return txErr
		}
		d.Payments, txErr = store.ConsolePayments(ctx, tx, store.PaymentFilter{Invoice: &id, Limit: 200})
		return txErr
	})
	return d, err
}

// Payments lists every account's payment attempts through q, newest
// first.
func (s *Service) Payments(ctx context.Context, q PaymentQuery, after string, limit int) (PaymentPage, error) {
	if !outcomes[q.Outcome] {
		return PaymentPage{}, badField("outcome", "approved, declined, no_method, manual or duplicate")
	}
	if !gatewayName.MatchString(q.Gateway) {
		return PaymentPage{}, badField("gateway", "a gateway's name")
	}
	limit, at, id, err := page(after, limit)
	if err != nil {
		return PaymentPage{}, err
	}
	var rows []ConsolePayment
	if err = postgres.InTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		rows, txErr = store.ConsolePayments(ctx, tx, store.PaymentFilter{Outcome: q.Outcome, Gateway: q.Gateway, BeforeAt: at, BeforeID: id,
			Limit: limit + 1})
		return txErr
	}); err != nil {
		return PaymentPage{}, err
	}
	out := PaymentPage{}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		out.NextCursor = cursor.Encode(last.CreatedAt, last.ID)
	}
	out.Items = rows
	return out, nil
}

// Totals is the console's billing summary now.
func (s *Service) Totals(ctx context.Context) (Totals, error) {
	cairo, err := time.LoadLocation("Africa/Cairo")
	if err != nil {
		return Totals{}, fmt.Errorf("billing: zone: %w", err)
	}
	now := s.now()
	local := now.In(cairo)
	t := Totals{MonthStart: time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, cairo),
		Open: []CurrencyTotal{}, Overdue: []CurrencyTotal{}, Collected: []CurrencyTotal{}, Failed: []FailedCount{}}
	var rows []store.SummaryRow
	if err = postgres.InTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		rows, txErr = store.ConsoleSummary(ctx, tx, now, t.MonthStart, now.Add(-7*24*time.Hour))
		return txErr
	}); err != nil {
		return Totals{}, err
	}
	for _, r := range rows {
		c := CurrencyTotal{Currency: r.Currency, Count: r.Count, TotalMinor: r.TotalMinor, TaxMinor: r.TaxMinor}
		switch r.Kind {
		case "open":
			t.Open = append(t.Open, c)
		case "overdue":
			t.Overdue = append(t.Overdue, c)
		case "collected":
			t.Collected = append(t.Collected, c)
		case "failed":
			t.Failed = append(t.Failed, FailedCount{Outcome: r.Outcome, Reason: r.Reason, Count: r.Count})
		}
	}
	for _, list := range [][]CurrencyTotal{t.Open, t.Overdue, t.Collected} {
		slices.SortFunc(list, func(a, b CurrencyTotal) int { return strings.Compare(a.Currency, b.Currency) })
	}
	slices.SortFunc(t.Failed, func(a, b FailedCount) int {
		if a.Count != b.Count {
			return cmp.Compare(b.Count, a.Count)
		}
		return strings.Compare(a.Outcome+a.Reason, b.Outcome+b.Reason)
	})
	return t, nil
}

// PaymentsByReference lists the payment attempts whose gateway reference
// is exactly reference (at most 10), newest first: the console's search.
func (s *Service) PaymentsByReference(ctx context.Context, reference string) (out []ConsolePayment, err error) {
	reference = strings.TrimSpace(reference)
	if reference == "" || len(reference) > 120 {
		return []ConsolePayment{}, nil
	}
	err = postgres.InTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		out, txErr = store.PaymentsByReference(ctx, tx, reference, 10)
		return txErr
	})
	return out, err
}
