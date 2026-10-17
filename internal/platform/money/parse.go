package money

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// maxParseLen bounds work on untrusted input. The longest valid value,
// "-92,233,720,368,547,758.08", is 26 runes.
const maxParseLen = 64

// Parse reads a decimal amount in major units for currency c, e.g.
// "1,234.50" -> 123450 minor units of EGP. It accepts:
//   - an optional leading '+' or '-';
//   - ASCII, Arabic-Indic (٠-٩) and extended Arabic-Indic (۰-۹) digits;
//   - thousands separators ',' or '٬' in correct groups of three;
//   - a decimal separator '.' or '٫' followed by 1..MinorUnits digits.
//
// It rejects exponents, currency symbols, extra fraction digits (no silent
// rounding) and values outside int64.
func Parse(s string, c Currency) (Money, error) {
	info, ok := c.info()
	if !ok {
		return Money{}, fmt.Errorf("%w: %q", ErrUnknownCurrency, string(c))
	}
	s = strings.TrimSpace(s)
	if s == "" || utf8.RuneCountInString(s) > maxParseLen {
		return Money{}, fmt.Errorf("%w: empty or too long", ErrSyntax)
	}

	neg := false
	switch s[0] {
	case '-':
		neg = true
		s = s[1:]
	case '+':
		s = s[1:]
	}

	var (
		mag        uint64
		digits     int  // digits in the current integer group or fraction
		intDigits  int  // total integer digits
		grouped    bool // saw a thousands separator
		inFraction bool
		fracDigits int
	)
	for _, r := range s {
		if d, ok := asciiDigit(r); ok {
			if inFraction {
				fracDigits++
				if fracDigits > info.minor {
					return Money{}, fmt.Errorf("%w: more than %d fraction digits", ErrSyntax, info.minor)
				}
			} else {
				intDigits++
				digits++
				if grouped && digits > 3 {
					return Money{}, fmt.Errorf("%w: bad digit grouping", ErrSyntax)
				}
			}
			var err error
			if mag, err = mulAdd(mag, 10, d); err != nil {
				return Money{}, err
			}
			continue
		}
		switch r {
		case ',', arThousands:
			// A group separator needs 1-3 digits before the first one and
			// exactly 3 between later ones.
			if inFraction || digits == 0 || digits > 3 || (grouped && digits != 3) {
				return Money{}, fmt.Errorf("%w: bad digit grouping", ErrSyntax)
			}
			grouped = true
			digits = 0
		case '.', arDecimal:
			if inFraction || (grouped && digits != 3) {
				return Money{}, fmt.Errorf("%w: misplaced decimal separator", ErrSyntax)
			}
			inFraction = true
		default:
			return Money{}, fmt.Errorf("%w: unexpected character %q", ErrSyntax, r)
		}
	}
	switch {
	case intDigits == 0 && fracDigits == 0:
		return Money{}, fmt.Errorf("%w: no digits", ErrSyntax)
	case inFraction && fracDigits == 0:
		return Money{}, fmt.Errorf("%w: decimal separator without digits", ErrSyntax)
	case !inFraction && grouped && digits != 3:
		return Money{}, fmt.Errorf("%w: bad digit grouping", ErrSyntax)
	}

	for range info.minor - fracDigits {
		var err error
		if mag, err = mulAdd(mag, 10, 0); err != nil {
			return Money{}, err
		}
	}
	amount, err := signed(mag, neg)
	if err != nil {
		return Money{}, err
	}
	return Money{amount: amount, currency: c}, nil
}

// mulAdd returns v*m + a, failing once the magnitude exceeds 2^63 (the
// largest magnitude any int64 can hold, for MinInt64).
func mulAdd(v, m, a uint64) (uint64, error) {
	const limit = uint64(1) << 63
	if v > (limit-a)/m {
		return 0, ErrOverflow
	}
	return v*m + a, nil
}
