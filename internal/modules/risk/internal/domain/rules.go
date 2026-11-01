package domain

import (
	"fmt"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// Action is what a store's rules do to a new storefront cash order
// (ADR-0066).
type Action string

// Actions.
const (
	NoAction Action = ""
	Hold     Action = "hold"   // waits for the merchant's review, without a confirmation message
	Verify   Action = "verify" // the customer's own yes does not confirm it: the merchant calls
	Prepay   Action = "prepay" // the shopper pays the delivery upfront by card (ADR-0067)
)

// Off is a rule that never applies.
const Off Band = "off"

// Rules are a store's risk rules: from which band an order is held,
// from which band its shopper prepays the delivery, and from which band
// it is verified by phone. Each is Off, BandMedium or BandHigh. The zero
// value is all off.
type Rules struct {
	HoldFrom   Band
	VerifyFrom Band
	PrepayFrom Band
}

// Normalized fills the zero value's rules as off.
func (r Rules) Normalized() Rules {
	if r.HoldFrom == "" {
		r.HoldFrom = Off
	}
	if r.VerifyFrom == "" {
		r.VerifyFrom = Off
	}
	if r.PrepayFrom == "" {
		r.PrepayFrom = Off
	}
	return r
}

// Validate checks merchant input: each rule is off, medium or high.
func (r Rules) Validate() error {
	var fields []errs.FieldError
	for _, f := range []struct {
		name string
		b    Band
	}{{"hold_from", r.HoldFrom}, {"verify_from", r.VerifyFrom}, {"prepay_from", r.PrepayFrom}} {
		switch f.b {
		case Off, BandMedium, BandHigh:
		default:
			fields = append(fields, errs.FieldError{Field: f.name, Code: "invalid", Message: fmt.Sprintf("%q: use off, medium or high", f.b)})
		}
	}
	if len(fields) > 0 {
		return errs.Validation(fields...)
	}
	return nil
}

// Active reports whether any rule applies to some band.
func (r Rules) Active() bool {
	r = r.Normalized()
	return r.HoldFrom != Off || r.VerifyFrom != Off || r.PrepayFrom != Off
}

// Decide is what the rules do to an order whose customer has band b: a
// hold first, then a prepaid delivery, then a verification; nothing for
// a new customer.
func (r Rules) Decide(b Band) Action {
	r = r.Normalized()
	switch {
	case b == BandNew:
		return NoAction
	case r.HoldFrom != Off && b.AtLeast(r.HoldFrom):
		return Hold
	case r.PrepayFrom != Off && b.AtLeast(r.PrepayFrom):
		return Prepay
	case r.VerifyFrom != Off && b.AtLeast(r.VerifyFrom):
		return Verify
	}
	return NoAction
}
