package domain

import (
	"fmt"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// FeeRule is a plan's pay-as-you-sell fee in one currency: a rate of the
// order's goods plus a fixed amount per order (ADR-0090), bounded below
// and above.
type FeeRule struct {
	RateBps    int32  // basis points: 250 is 2.5%
	FixedMinor int64  // added to every delivered order's fee: $0.04 is 4
	MinMinor   int64  // the least fee of a delivered order
	MaxMinor   *int64 // the most; nil is no bound
}

// Fee is the fee on base, the order's goods after their discount (the
// approved base: no shipping, no COD fee), in minor units of the rule's
// currency: the rate rounded half up plus the fixed amount, within the
// bounds, and never more than base.
func (r FeeRule) Fee(base int64) int64 {
	if base <= 0 {
		return 0
	}
	// base is at most ~10^15 minor units, so base*10000 fits an int64.
	fee := (base*int64(r.RateBps)+5000)/10000 + r.FixedMinor
	fee = max(fee, r.MinMinor)
	if r.MaxMinor != nil {
		fee = min(fee, *r.MaxMinor)
	}
	return min(fee, base)
}

// PeriodOf is the month t falls in, UTC, as quotas count (ADR-0038): the
// month a fee is invoiced for.
func PeriodOf(t time.Time) time.Time {
	y, m, _ := t.UTC().Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
}

// FeeStatus is where a delivered order's fee stands.
type FeeStatus string

// Fee statuses.
const (
	FeeDue       FeeStatus = "due"        // to be invoiced
	FeeCancelled FeeStatus = "cancelled"  // returned before it was invoiced
	FeeInvoiced  FeeStatus = "invoiced"   // on an invoice
	FeeCreditDue FeeStatus = "credit_due" // returned after it was invoiced: credited on the next one
	FeeCredited  FeeStatus = "credited"
)

// ParseFeeStatus validates a stored status.
func ParseFeeStatus(v string) (FeeStatus, error) {
	switch s := FeeStatus(v); s {
	case FeeDue, FeeCancelled, FeeInvoiced, FeeCreditDue, FeeCredited:
		return s, nil
	}
	return "", fmt.Errorf("billing: unknown fee status %q", v)
}

// SellFee is the fee on one delivered order, in its account's currency.
// An order in another currency is converted at the rate of its delivery
// (ADR-0090): BaseMinor is its goods converted, and the order's own are
// kept beside.
type SellFee struct {
	OrderID    ulid.ID
	Period     time.Time
	PlanID     ulid.ID
	BaseMinor  int64
	RateBps    int32
	FixedMinor int64
	FeeMinor   int64
	Currency   string
	// The order's currency and goods, and the rate they were converted at
	// (units of the order's currency per unit of Currency, times
	// money.RateScale); the same currency at par.
	OrderCurrency  string
	OrderBaseMinor int64
	RateMicros     int64
	Status         FeeStatus
	DeliveredAt    time.Time
	ReturnedAt     time.Time
	Prepaid        bool // taken from the prepaid balance (ADR-0084)
}

// Returned is the fee after its order came back at: no fee when it was
// not invoiced yet, a credit on the next invoice when it was. changed is
// false when the return was already applied.
func (f SellFee) Returned(at time.Time) (SellFee, bool) {
	switch f.Status {
	case FeeDue:
		f.Status = FeeCancelled
	case FeeInvoiced:
		f.Status = FeeCreditDue
	default:
		return f, false
	}
	f.ReturnedAt = at
	return f, true
}
