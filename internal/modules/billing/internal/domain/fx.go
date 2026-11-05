package domain

import (
	"fmt"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
)

// Exchange rates for the fees of orders in another currency than their
// account's (ADR-0090): the console sets each currency's rate to the
// dollar, and each fee keeps the rate it was converted at.

// maxPerUSD bounds a rate: a million units to the dollar. It keeps a rate
// between two currencies within an int64 (RateBetween).
const maxPerUSD = 1_000_000 * money.RateScale

// FXRate is a currency's rate to the dollar: its units per dollar, times
// money.RateScale (the riyal is 3_750_000).
type FXRate struct {
	Currency     string
	PerUSDMicros int64
}

// Validate checks a rate the console saves: a store currency other than
// the dollar, at more than zero and at most a million to the dollar.
func (r FXRate) Validate() error {
	var fe []errs.FieldError
	if c := money.Currency(r.Currency); !c.Valid() || c == money.USD {
		fe = append(fe, errs.FieldError{Field: "currency", Code: "invalid", Message: "a store currency other than USD"})
	}
	if r.PerUSDMicros <= 0 || r.PerUSDMicros > maxPerUSD {
		fe = append(fe, errs.FieldError{Field: "per_usd_micros", Code: "out_of_range", Message: "more than 0, at most 1,000,000 to the dollar"})
	}
	if len(fe) > 0 {
		return errs.Validation(fe...)
	}
	return nil
}

// RateBetween is the rate an amount in from converts to to at: the units
// of from per unit of to, times money.RateScale. The same currency is at
// par; ok is false when either has no rate.
func RateBetween(rates []FXRate, from, to string) (int64, bool) {
	if from == to {
		return money.RateScale, true
	}
	perUSD := func(cur string) (int64, bool) {
		if cur == string(money.USD) {
			return money.RateScale, true
		}
		for _, r := range rates {
			if r.Currency == cur {
				return r.PerUSDMicros, true
			}
		}
		return 0, false
	}
	f, okF := perUSD(from)
	t, okT := perUSD(to)
	if !okF || !okT || t <= 0 {
		return 0, false
	}
	// f ≤ 10^12 and RateScale is 10^6, so f*RateScale fits an int64.
	return (f*money.RateScale + t/2) / t, true
}

// Charge is the fee of an order whose goods are base, billed in billed at
// rate (RateBetween): the goods converted, rounded half up, and the
// rule's fee on them.
func (r FeeRule) Charge(base money.Money, billed string, rate int64) (converted, fee int64, err error) {
	c, err := base.Convert(money.Currency(billed), rate, money.RoundHalfUp)
	if err != nil {
		return 0, 0, fmt.Errorf("billing: convert %s to %s: %w", base.Currency(), billed, err)
	}
	return c.Amount(), r.Fee(c.Amount()), nil
}
