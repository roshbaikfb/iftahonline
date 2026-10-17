// Package money represents monetary amounts as int64 minor units plus an
// ISO 4217 currency. It never uses floating point (CLAUDE.md rule 7).
//
// Every arithmetic operation is checked: overflow and currency mismatches
// are returned as errors, never silently wrapped. The zero Money has no
// currency and is rejected by all operations; construct values with New,
// Zero or Parse.
package money

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/bits"
)

// Sentinel errors. Match with errors.Is.
var (
	ErrUnknownCurrency  = errors.New("money: unknown currency")
	ErrCurrencyMismatch = errors.New("money: currency mismatch")
	ErrOverflow         = errors.New("money: amount overflows int64")
	ErrInvalidRatio     = errors.New("money: invalid allocation ratios")
	ErrInvalidRounding  = errors.New("money: invalid rounding mode")
	ErrSyntax           = errors.New("money: invalid amount syntax")
)

// Currency is an ISO 4217 code supported by the platform.
type Currency string

// Supported currencies.
const (
	EGP Currency = "EGP" // Egyptian pound
	SAR Currency = "SAR" // Saudi riyal
	AED Currency = "AED" // UAE dirham
)

type currencyInfo struct {
	minor    int    // digits after the decimal point
	scale    uint64 // 10^minor
	symbolAR string
}

// info is a switch rather than a map so there is no package-level state.
// Add a case here (and to Currencies) to support a new currency.
func (c Currency) info() (currencyInfo, bool) {
	switch c {
	case EGP:
		return currencyInfo{minor: 2, scale: 100, symbolAR: "ج.م."}, true
	case SAR:
		return currencyInfo{minor: 2, scale: 100, symbolAR: "ر.س."}, true
	case AED:
		return currencyInfo{minor: 2, scale: 100, symbolAR: "د.إ."}, true
	default:
		return currencyInfo{}, false
	}
}

// Currencies lists every supported currency.
func Currencies() []Currency { return []Currency{EGP, SAR, AED} }

// Valid reports whether c is a supported currency.
func (c Currency) Valid() bool {
	_, ok := c.info()
	return ok
}

// MinorUnits is the number of fraction digits (2 for EGP/SAR/AED).
// It returns 0 for an unsupported currency.
func (c Currency) MinorUnits() int {
	i, _ := c.info()
	return i.minor
}

// Money is an immutable amount in minor units of a currency.
// It is comparable with == (same amount and currency).
type Money struct {
	amount   int64
	currency Currency
}

// New returns amount minor units of currency c.
func New(amount int64, c Currency) (Money, error) {
	if !c.Valid() {
		return Money{}, fmt.Errorf("%w: %q", ErrUnknownCurrency, string(c))
	}
	return Money{amount: amount, currency: c}, nil
}

// Zero returns a zero amount of currency c.
func Zero(c Currency) (Money, error) { return New(0, c) }

// Amount returns the value in minor units (e.g. piastres, halalas, fils).
func (m Money) Amount() int64 { return m.amount }

// Currency returns the currency.
func (m Money) Currency() Currency { return m.currency }

// IsZero reports whether the amount is zero.
func (m Money) IsZero() bool { return m.amount == 0 }

// IsNegative reports whether the amount is below zero.
func (m Money) IsNegative() bool { return m.amount < 0 }

func (m Money) valid() error {
	if !m.currency.Valid() {
		return fmt.Errorf("%w: %q", ErrUnknownCurrency, string(m.currency))
	}
	return nil
}

func (m Money) sameCurrency(o Money) error {
	if err := m.valid(); err != nil {
		return err
	}
	if err := o.valid(); err != nil {
		return err
	}
	if m.currency != o.currency {
		return fmt.Errorf("%w: %s vs %s", ErrCurrencyMismatch, m.currency, o.currency)
	}
	return nil
}

// Add returns m + o.
func (m Money) Add(o Money) (Money, error) {
	if err := m.sameCurrency(o); err != nil {
		return Money{}, err
	}
	s := m.amount + o.amount
	// Overflow iff both operands have the same sign and the result's sign differs.
	if (m.amount >= 0) == (o.amount >= 0) && (s >= 0) != (m.amount >= 0) {
		return Money{}, ErrOverflow
	}
	return Money{amount: s, currency: m.currency}, nil
}

// Sub returns m - o.
func (m Money) Sub(o Money) (Money, error) {
	if err := m.sameCurrency(o); err != nil {
		return Money{}, err
	}
	d := m.amount - o.amount
	// Overflow iff operands have different signs and the result's sign differs from m.
	if (m.amount >= 0) != (o.amount >= 0) && (d >= 0) != (m.amount >= 0) {
		return Money{}, ErrOverflow
	}
	return Money{amount: d, currency: m.currency}, nil
}

// Neg returns -m.
func (m Money) Neg() (Money, error) {
	if err := m.valid(); err != nil {
		return Money{}, err
	}
	if m.amount == math.MinInt64 {
		return Money{}, ErrOverflow
	}
	return Money{amount: -m.amount, currency: m.currency}, nil
}

// MulInt returns m * n (e.g. unit price times quantity).
func (m Money) MulInt(n int64) (Money, error) {
	if err := m.valid(); err != nil {
		return Money{}, err
	}
	p, err := mulDiv(m.amount, n, 1, RoundDown)
	if err != nil {
		return Money{}, err
	}
	return Money{amount: p, currency: m.currency}, nil
}

// Cmp returns -1, 0 or +1 as m is less than, equal to or greater than o.
func (m Money) Cmp(o Money) (int, error) {
	if err := m.sameCurrency(o); err != nil {
		return 0, err
	}
	switch {
	case m.amount < o.amount:
		return -1, nil
	case m.amount > o.amount:
		return 1, nil
	default:
		return 0, nil
	}
}

// RoundingMode selects how fractional minor units are resolved.
type RoundingMode int

// Rounding modes.
const (
	// RoundHalfUp rounds half away from zero (common for VAT invoices).
	RoundHalfUp RoundingMode = iota + 1
	// RoundHalfEven rounds half to even (banker's rounding).
	RoundHalfEven
	// RoundDown truncates toward zero.
	RoundDown
)

// PercentBP returns m * bp / 10_000, where bp is basis points
// (1400 = 14%), rounded with mode. Use it for tax and percentage discounts.
func (m Money) PercentBP(bp int64, mode RoundingMode) (Money, error) {
	if err := m.valid(); err != nil {
		return Money{}, err
	}
	v, err := mulDiv(m.amount, bp, 10_000, mode)
	if err != nil {
		return Money{}, err
	}
	return Money{amount: v, currency: m.currency}, nil
}

// Allocate splits m in proportion to ratios without losing a minor unit:
// the parts always sum to m. Leftover units go one each to the first parts.
func (m Money) Allocate(ratios ...int64) ([]Money, error) {
	if err := m.valid(); err != nil {
		return nil, err
	}
	if len(ratios) == 0 {
		return nil, ErrInvalidRatio
	}
	var total int64
	for _, r := range ratios {
		if r < 0 {
			return nil, ErrInvalidRatio
		}
		if total > math.MaxInt64-r {
			return nil, ErrOverflow
		}
		total += r
	}
	if total == 0 {
		return nil, ErrInvalidRatio
	}
	parts := make([]Money, len(ratios))
	remainder := m.amount
	for i, r := range ratios {
		// |amount*r/total| <= |amount|, so this cannot overflow.
		share, err := mulDiv(m.amount, r, total, RoundDown)
		if err != nil {
			return nil, err
		}
		parts[i] = Money{amount: share, currency: m.currency}
		remainder -= share
	}
	step := int64(1)
	if remainder < 0 {
		step = -1
	}
	for i := 0; remainder != 0; i++ {
		parts[i%len(parts)].amount += step
		remainder -= step
	}
	return parts, nil
}

// mulDiv computes a*b/d with a 128-bit intermediate and the given rounding.
// d must be > 0.
func mulDiv(a, b, d int64, mode RoundingMode) (int64, error) {
	if mode != RoundHalfUp && mode != RoundHalfEven && mode != RoundDown {
		return 0, ErrInvalidRounding
	}
	neg := (a < 0) != (b < 0)
	hi, lo := bits.Mul64(absU(a), absU(b))
	ud := uint64(d) //nolint:gosec // d > 0 is guaranteed by callers.
	if hi >= ud {
		return 0, ErrOverflow
	}
	q, r := bits.Div64(hi, lo, ud)
	if r != 0 && mode != RoundDown {
		half := ud - r // compare 2r with d without overflowing
		switch {
		case r > half:
			q++
		case r == half && (mode == RoundHalfUp || q%2 == 1):
			q++
		}
	}
	return signed(q, neg)
}

func absU(v int64) uint64 {
	if v < 0 {
		return uint64(^v) + 1 //nolint:gosec // ^v >= 0 when v < 0; correct for MinInt64 too.
	}
	return uint64(v)
}

// signed converts a magnitude back to int64, allowing exactly MinInt64.
func signed(mag uint64, neg bool) (int64, error) {
	if neg {
		if mag > 1<<63 {
			return 0, ErrOverflow
		}
		return int64(-mag), nil //nolint:gosec // range checked above; two's complement wrap is intended.
	}
	if mag > math.MaxInt64 {
		return 0, ErrOverflow
	}
	return int64(mag), nil
}

type jsonMoney struct {
	Amount   *int64   `json:"amount"`
	Currency Currency `json:"currency"`
}

// MarshalJSON encodes {"amount":<minor units>,"currency":"EGP"}.
func (m Money) MarshalJSON() ([]byte, error) {
	if err := m.valid(); err != nil {
		return nil, err
	}
	b, err := json.Marshal(jsonMoney{Amount: &m.amount, Currency: m.currency})
	if err != nil {
		return nil, fmt.Errorf("money: marshal: %w", err)
	}
	return b, nil
}

// UnmarshalJSON decodes and validates the MarshalJSON format.
func (m *Money) UnmarshalJSON(b []byte) error {
	var j jsonMoney
	if err := json.Unmarshal(b, &j); err != nil {
		return fmt.Errorf("money: unmarshal: %w", err)
	}
	if j.Amount == nil {
		return fmt.Errorf("%w: missing amount", ErrSyntax)
	}
	v, err := New(*j.Amount, j.Currency)
	if err != nil {
		return err
	}
	*m = v
	return nil
}
