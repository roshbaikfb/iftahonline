package domain

import (
	"fmt"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// The founding-merchants program (approved 2026-11-02): the first
// paying merchants keep a discount for life, and their price, while they
// stay subscribed.

// Program is the program's settings the console edits.
type Program struct {
	Enabled bool
	// Seats is how many merchants can join; seats are never reused.
	Seats int
	// DiscountBps is the discount new members keep (2000 = 20%).
	DiscountBps int32
}

// Validate checks the settings.
func (p Program) Validate() error {
	var fe []errs.FieldError
	if p.Seats < 1 || p.Seats > 10000 {
		fe = append(fe, errs.FieldError{Field: "seats", Code: "invalid", Message: "1 to 10000"})
	}
	if p.DiscountBps < 100 || p.DiscountBps > 9000 {
		fe = append(fe, errs.FieldError{Field: "discount_bps", Code: "invalid", Message: "1% to 90%"})
	}
	if len(fe) > 0 {
		return errs.Validation(fe...)
	}
	return nil
}

// Seats are the program as the site shows it: its settings and the
// seats taken (lost ones stay taken).
type Seats struct {
	Program
	Taken int
}

// Left are the seats a new merchant can still take.
func (s Seats) Left() int { return max(0, s.Seats-s.Taken) }

// Open says whether the site invites merchants to join: on, with seats
// left.
func (s Seats) Open() bool { return s.Enabled && s.Left() > 0 }

// Lost is why a member lost the program.
type Lost string

// The reasons.
const (
	LostCancelled Lost = "cancelled" // left its subscription
	LostUnpaid    Lost = "unpaid"    // a renewal unpaid past UnpaidGrace
)

// UnpaidGrace is how long a renewal may stay unpaid past its due date
// before the membership ends (the approved terms: 30 days).
const UnpaidGrace = 30 * 24 * time.Hour

// Member is a store in the program.
type Member struct {
	StoreID, OwnerID ulid.ID
	Seat             int
	DiscountBps      int32
	// The locked price: the store renews this plan and cycle in this
	// currency at LockedMinor at most.
	Currency    string
	PlanCode    string
	Cycle       Cycle
	LockedMinor int64
	JoinedAt    time.Time
	LostAt      time.Time // zero while a member
	LostReason  Lost
}

// Active says whether the store keeps the program's terms.
func (m Member) Active() bool { return m.LostAt.IsZero() }

// Renewal is what a member pays for a renewal of code and cycle in
// currency whose price is now current: the lower of its locked price and
// the current one when it renews the same plan and cycle, else the
// current price, which becomes its new lock (relock); then its discount.
func (m Member) Renewal(code string, cycle Cycle, currency string, current int64) (charged, discount int64, relock bool) {
	charged = current
	if code == m.PlanCode && cycle == m.Cycle && currency == m.Currency {
		charged = min(current, m.LockedMinor)
	} else {
		relock = true
	}
	return charged, Discount(charged, m.DiscountBps), relock
}

// Discount is bps of amount, rounded half up.
func Discount(amount int64, bps int32) int64 {
	if amount <= 0 || bps <= 0 {
		return 0
	}
	return (amount*int64(bps) + 5000) / 10000
}

// DiscountLine is the invoice line of a founding merchant's discount.
func DiscountLine(store ulid.ID, discount int64, bps int32) Line {
	return Line{Kind: LineFounding, StoreID: store, Quantity: 1, AmountMinor: -discount,
		Description: fmt.Sprintf("خصم التاجر المؤسس (%s)", Percent(bps))}
}

// Percent writes basis points as a percentage: 2000 → "20%", 2050 →
// "20.5%".
func Percent(bps int32) string {
	if bps%100 == 0 {
		return fmt.Sprintf("%d%%", bps/100)
	}
	return fmt.Sprintf("%d.%02d%%", bps/100, bps%100)
}
