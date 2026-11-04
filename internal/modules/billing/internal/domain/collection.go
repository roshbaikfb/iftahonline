package domain

import (
	"fmt"
	"strings"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// How pay-as-you-sell fees are collected (ADR-0083, approved 2026-11-04).

// Collection is a way an account pays its fees.
type Collection string

// Collections.
const (
	CollectBalance Collection = "balance" // a prepaid balance, a fee taken per delivered order
	CollectCard    Collection = "card"    // the card on file, charged at the minimum or the month's end
)

// Minimum bounds, in minor units: 1.00 to 1,000,000.00.
const (
	minMinimum = 100
	maxMinimum = 100_000_000
)

// CollectionSettings are the console's: the ways merchants may choose,
// and the minimum per currency. The minimum is the least top-up and the
// card's threshold; the low-balance notice is at half of it.
type CollectionSettings struct {
	Prepaid  bool
	Card     bool
	Minimums map[string]int64 // by currency: EGP, SAR, AED
}

// Currencies are the platform's, in the order the console shows them.
func Currencies() []string { return []string{"EGP", "SAR", "AED"} }

// Validate checks every currency has a minimum within bounds.
func (c CollectionSettings) Validate() error {
	var fe []errs.FieldError
	for _, cur := range Currencies() {
		if m, ok := c.Minimums[cur]; !ok || m < minMinimum || m > maxMinimum {
			fe = append(fe, errs.FieldError{Field: "minimums." + cur, Code: "out_of_range", Message: "between 1 and 1,000,000"})
		}
	}
	if len(c.Minimums) != len(Currencies()) && len(fe) == 0 {
		fe = append(fe, errs.FieldError{Field: "minimums", Code: "invalid", Message: "one minimum per currency: EGP, SAR, AED"})
	}
	if len(fe) > 0 {
		return errs.Validation(fe...)
	}
	return nil
}

// Offered are the ways a merchant may choose now. Turning a way off
// changes nothing for accounts that chose it (approved 2026-11-04).
func (c CollectionSettings) Offered() []Collection {
	var out []Collection
	if c.Prepaid {
		out = append(out, CollectBalance)
	}
	if c.Card {
		out = append(out, CollectCard)
	}
	return out
}

// BalanceLevel is where a prepaid balance stands.
type BalanceLevel string

// Balance levels.
const (
	BalanceOK   BalanceLevel = "ok"
	BalanceLow  BalanceLevel = "low"  // under half the minimum
	BalanceDebt BalanceLevel = "debt" // nothing left, or owed
)

// LevelOf is the level of balance for a currency's minimum.
func LevelOf(balance, minimum int64) BalanceLevel {
	switch {
	case balance <= 0:
		return BalanceDebt
	case balance < minimum/2:
		return BalanceLow
	}
	return BalanceOK
}

// EntryKind is what changed a balance.
type EntryKind string

// Entry kinds.
const (
	EntryTopUp     EntryKind = "topup"      // paid on the gateway's page
	EntryManual    EntryKind = "manual"     // added in the console
	EntryFee       EntryKind = "fee"        // a delivered order's fee, with its tax
	EntryFeeRefund EntryKind = "fee_refund" // that fee back, the order returned
)

// CreditReason is why the console adds to a balance.
type CreditReason string

// Credit reasons.
const (
	ReasonTransfer     CreditReason = "transfer"     // a bank transfer the platform confirmed
	ReasonCompensation CreditReason = "compensation" // the platform's goodwill
	ReasonCorrection   CreditReason = "correction"   // a mistake put right
)

// maxCredit bounds one manual credit: 1,000,000.00.
const maxCredit = 100_000_000

// Credit is an amount the console adds to a balance.
type Credit struct {
	AmountMinor int64
	Reason      CreditReason
	Reference   string
}

// ParseCredit checks a credit from input; the reference is trimmed.
func ParseCredit(amount int64, reason, reference string) (Credit, error) {
	c := Credit{AmountMinor: amount, Reason: CreditReason(reason), Reference: strings.TrimSpace(reference)}
	var fe []errs.FieldError
	if amount <= 0 || amount > maxCredit {
		fe = append(fe, errs.FieldError{Field: "amount_minor", Code: "out_of_range", Message: "between 0.01 and 1,000,000"})
	}
	switch c.Reason {
	case ReasonTransfer, ReasonCompensation, ReasonCorrection:
	default:
		fe = append(fe, errs.FieldError{Field: "reason", Code: "invalid", Message: fmt.Sprintf("unknown reason %q", reason)})
	}
	if c.Reference == "" || len(c.Reference) > 120 {
		fe = append(fe, errs.FieldError{Field: "reference", Code: "required", Message: "1 to 120 characters"})
	}
	if len(fe) > 0 {
		return Credit{}, errs.Validation(fe...)
	}
	return c, nil
}
