// Package domain holds the rules of coupons (ADR-0042): what a coupon
// takes off an order, and when it applies.
package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Kind is what a coupon takes off.
type Kind string

// Kinds.
const (
	// Percent takes a share of the items' subtotal.
	Percent Kind = "percent"
	// Fixed takes an amount off the items' subtotal, at most all of it.
	Fixed Kind = "fixed"
	// FreeShipping waives the delivery rate (the COD fee stays).
	FreeShipping Kind = "free_shipping"
)

// Valid reports whether k is a kind.
func (k Kind) Valid() bool { return k == Percent || k == Fixed || k == FreeShipping }

// Code bounds.
const (
	MinCodeLen = 3
	MaxCodeLen = 32
)

// NormalizeCode reads a code as a shopper types it: trimmed, upper case;
// ok is false unless 3 to 32 of A-Z, 0-9, "-" and "_" remain.
func NormalizeCode(s string) (string, bool) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if len(s) < MinCodeLen || len(s) > MaxCodeLen {
		return "", false
	}
	for _, r := range s {
		if ok := r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_'; !ok {
			return "", false
		}
	}
	return s, true
}

// Coupon is a store's discount code.
type Coupon struct {
	ID   ulid.ID
	Code string
	Kind Kind
	// Percent is Percent's share, 1 to 100.
	Percent int
	// AmountMinor is Fixed's amount, in the store's currency.
	AmountMinor int64
	// MinSubtotalMinor is the least items' subtotal it applies to (0:
	// any).
	MinSubtotalMinor int64
	// MaxUses bounds its uses by every customer, MaxPerCustomer by one
	// (a phone number); nil is no bound.
	MaxUses        *int
	MaxPerCustomer *int
	// StartsAt and EndsAt bound when it applies; nil is open.
	StartsAt *time.Time
	EndsAt   *time.Time
	Active   bool
	// Uses counts orders holding it (a cancelled order before shipping
	// gives its use back).
	Uses      int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Limits on a coupon's fields.
const (
	MaxAmountMinor = 100_000_000 // 1,000,000.00
	MaxUsesBound   = 1_000_000
)

// Validate checks a coupon the merchant writes.
func (c Coupon) Validate() error {
	var fields []errs.FieldError
	bad := func(field, code, msg string) {
		fields = append(fields, errs.FieldError{Field: field, Code: code, Message: msg})
	}
	if _, ok := NormalizeCode(c.Code); !ok {
		bad("code", "invalid", "3 to 32 letters A-Z, digits, - or _")
	}
	switch c.Kind {
	case Percent:
		if c.Percent < 1 || c.Percent > 100 {
			bad("percent", "range", "percent must be 1 to 100")
		}
	case Fixed:
		if c.AmountMinor < 1 || c.AmountMinor > MaxAmountMinor {
			bad("amount_minor", "range", "amount must be more than 0")
		}
	case FreeShipping:
	default:
		bad("kind", "invalid", "percent, fixed or free_shipping")
	}
	if c.MinSubtotalMinor < 0 || c.MinSubtotalMinor > MaxAmountMinor {
		bad("min_subtotal_minor", "range", "minimum subtotal must be 0 or more")
	}
	if c.MaxUses != nil && (*c.MaxUses < 1 || *c.MaxUses > MaxUsesBound) {
		bad("max_uses", "range", "at least 1")
	}
	if c.MaxPerCustomer != nil && (*c.MaxPerCustomer < 1 || *c.MaxPerCustomer > MaxUsesBound) {
		bad("max_per_customer", "range", "at least 1")
	}
	if c.StartsAt != nil && c.EndsAt != nil && !c.EndsAt.After(*c.StartsAt) {
		bad("ends_at", "range", "ends after it starts")
	}
	if len(fields) > 0 {
		return errs.Validation(fields...)
	}
	return nil
}

// Reason is why a coupon does not apply.
type Reason string

// Reasons, as the shopper is told them.
const (
	NotFound    Reason = "not_found"    // no such code (or the field is malformed)
	Inactive    Reason = "inactive"     // the merchant turned it off
	NotStarted  Reason = "not_started"  // before StartsAt
	Expired     Reason = "expired"      // after EndsAt
	MinSubtotal Reason = "min_subtotal" // the items cost less than its minimum
	UsedUp      Reason = "used_up"      // MaxUses reached
	PerCustomer Reason = "per_customer" // this customer used it MaxPerCustomer times
)

// Rejected is a coupon that does not apply to an order.
type Rejected struct {
	Reason Reason
	// MinSubtotalMinor is the minimum, for MinSubtotal.
	MinSubtotalMinor int64
}

func (r *Rejected) Error() string { return fmt.Sprintf("promotions: coupon rejected: %s", r.Reason) }

// ErrNotFound is a missing coupon (merchant API).
var ErrNotFound = errors.New("promotions: coupon not found")

// Discount is what a coupon takes off an order.
type Discount struct {
	CouponID ulid.ID
	Code     string
	// Amount comes off the items' subtotal.
	Amount money.Money
	// FreeShipping waives the delivery rate.
	FreeShipping bool
}

// Apply checks c for an order whose items cost subtotal, at now, by a
// customer who used it customerUses times (-1 when the customer is not
// known yet: a quote), and says what it takes off.
func (c Coupon) Apply(subtotal money.Money, now time.Time, customerUses int) (Discount, error) {
	reject := func(r Reason) (Discount, error) {
		return Discount{}, &Rejected{Reason: r, MinSubtotalMinor: c.MinSubtotalMinor}
	}
	switch {
	case !c.Active:
		return reject(Inactive)
	case c.StartsAt != nil && now.Before(*c.StartsAt):
		return reject(NotStarted)
	case c.EndsAt != nil && !now.Before(*c.EndsAt):
		return reject(Expired)
	case subtotal.Amount() < c.MinSubtotalMinor:
		return reject(MinSubtotal)
	case c.MaxUses != nil && c.Uses >= *c.MaxUses:
		return reject(UsedUp)
	case c.MaxPerCustomer != nil && customerUses >= *c.MaxPerCustomer:
		return reject(PerCustomer)
	}
	d := Discount{CouponID: c.ID, Code: c.Code}
	var off int64
	switch c.Kind {
	case Percent:
		off = subtotal.Amount() * int64(c.Percent) / 100 // rounds down: never more than promised
	case Fixed:
		off = min(c.AmountMinor, subtotal.Amount())
	case FreeShipping:
		d.FreeShipping = true
	}
	var err error
	if d.Amount, err = money.New(off, subtotal.Currency()); err != nil {
		return Discount{}, err
	}
	return d, nil
}
