package money_test

import (
	"errors"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/platform/money"
)

// FuzzMoneyParse: Parse never panics, and anything it accepts round-trips
// through both locales' FormatAmount back to the same value.
func FuzzMoneyParse(f *testing.F) {
	for _, s := range []string{
		"0", "1,234.50", "-92233720368547758.08", "92233720368547758.07",
		"١٬٢٣٤٫٥٠", "۱۲۳٫۴", ".5", "1.", "1,,2", "+-1", "\x00", "٫",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for _, c := range money.Currencies() {
			m, err := money.Parse(s, c)
			if err != nil {
				if !errors.Is(err, money.ErrSyntax) && !errors.Is(err, money.ErrOverflow) {
					t.Fatalf("Parse(%q) unexpected error class: %v", s, err)
				}
				continue
			}
			for _, l := range []money.Locale{money.LocaleEN, money.LocaleAR} {
				back, err := money.Parse(m.FormatAmount(l), c)
				if err != nil {
					t.Fatalf("re-parse of %q (from %q) failed: %v", m.FormatAmount(l), s, err)
				}
				if back != m {
					t.Fatalf("round trip %q -> %v -> %v", s, m, back)
				}
			}
		}
	})
}

// FuzzMoneyArith: arithmetic never panics, and successful results satisfy
// basic algebraic identities.
func FuzzMoneyArith(f *testing.F) {
	f.Add(int64(100), int64(-7), int64(1400), uint8(3))
	f.Add(int64(-9223372036854775808), int64(-1), int64(10000), uint8(1))
	f.Add(int64(9223372036854775807), int64(1), int64(-1), uint8(7))
	f.Fuzz(func(t *testing.T, a, b, bp int64, n uint8) {
		ma, _ := money.New(a, money.EGP)
		mb, _ := money.New(b, money.EGP)

		if sum, err := ma.Add(mb); err == nil {
			if diff, subErr := sum.Sub(mb); subErr != nil || diff != ma {
				t.Fatalf("(%d+%d)-%d = %v, %v", a, b, b, diff, subErr)
			}
		} else if !errors.Is(err, money.ErrOverflow) {
			t.Fatalf("Add: %v", err)
		}

		for _, mode := range []money.RoundingMode{money.RoundHalfUp, money.RoundHalfEven, money.RoundDown} {
			if _, err := ma.PercentBP(bp, mode); err != nil && !errors.Is(err, money.ErrOverflow) {
				t.Fatalf("PercentBP: %v", err)
			}
		}

		if _, err := ma.MulInt(b); err != nil && !errors.Is(err, money.ErrOverflow) {
			t.Fatalf("MulInt: %v", err)
		}

		ratios := make([]int64, int(n%8)+1)
		for i := range ratios {
			ratios[i] = int64(i+1) * (bp & 0xffff)
		}
		parts, err := ma.Allocate(ratios...)
		if err != nil {
			if !errors.Is(err, money.ErrInvalidRatio) && !errors.Is(err, money.ErrOverflow) {
				t.Fatalf("Allocate: %v", err)
			}
			return
		}
		total, _ := money.Zero(money.EGP)
		for _, p := range parts {
			if total, err = total.Add(p); err != nil {
				t.Fatalf("parts overflow: %v", err)
			}
		}
		if total != ma {
			t.Fatalf("Allocate(%d, %v) parts sum to %v", a, ratios, total)
		}
	})
}
