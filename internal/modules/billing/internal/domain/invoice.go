package domain

import (
	"fmt"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// AccountKind is who an account pays for.
type AccountKind string

// Account kinds.
const (
	AccountStore  AccountKind = "store"  // one store, its own
	AccountAgency AccountKind = "agency" // many stores, one invoice
)

// Account is who pays (ADR-0049).
type Account struct {
	ID       ulid.ID
	Kind     AccountKind
	Name     string
	Currency string
	Country  string
}

// CountryOf is the country of an account's currency: its tax's.
func CountryOf(currency string) string {
	switch currency {
	case "EGP":
		return "EG"
	case "SAR":
		return "SA"
	case "AED":
		return "AE"
	}
	return ""
}

// LineKind is what an invoice line bills.
type LineKind string

// Line kinds.
const (
	LineSubscription LineKind = "subscription" // a plan's next period
	LineSellFees     LineKind = "sell_fees"    // a month's pay-as-you-sell fees
	LineFeeCredit    LineKind = "fee_credit"   // fees of orders returned after they were invoiced
)

// Line is one line of an invoice.
type Line struct {
	Kind        LineKind
	StoreID     ulid.ID // the store it is for
	Description string
	Quantity    int64
	AmountMinor int64 // negative for a credit
}

// InvoiceTotals are an invoice's sums.
type InvoiceTotals struct {
	SubtotalMinor int64
	TaxBps        int32
	TaxMinor      int64
	TotalMinor    int64
}

// Totals sums lines and adds tax, rounded half up. ok is false when
// there is nothing to bill: no lines, or credits as large as the
// charges (the credits then wait for a later invoice).
func Totals(lines []Line, taxBps int32) (InvoiceTotals, bool) {
	var sub int64
	for _, l := range lines {
		sub += l.AmountMinor
	}
	if sub <= 0 {
		return InvoiceTotals{}, false
	}
	tax := (sub*int64(taxBps) + 5000) / 10000
	return InvoiceTotals{SubtotalMinor: sub, TaxBps: taxBps, TaxMinor: tax, TotalMinor: sub + tax}, true
}

// InvoiceNumber is the n-th invoice number of year: gapless, per year.
func InvoiceNumber(year int, n int64) string { return fmt.Sprintf("INV-%04d-%06d", year, n) }

// PaymentTerm is how long after issue an invoice is due.
const PaymentTerm = 3 * 24 * time.Hour

// DueAt is when an invoice issued at is due.
func DueAt(issued time.Time) time.Time { return issued.Add(PaymentTerm) }

// PreviousPeriod is the month before the one at falls in: what a run at
// the start of a month invoices.
func PreviousPeriod(at time.Time) time.Time { return PeriodOf(at).AddDate(0, -1, 0) }

// RenewsBy is when the periods a month's invoice renews end: the next
// month's first day. Paid periods are calendar months (ADR-0049).
func RenewsBy(period time.Time) time.Time { return period.AddDate(0, 1, 0) }
