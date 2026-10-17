// Package phone parses and normalises mobile numbers for Egypt and the GCC
// into E.164.
//
// The rules are hand-written for the seven supported countries instead of
// pulling in libphonenumber: we need mobiles only (OTP, WhatsApp, courier
// contact) and want no large dependency on the hot path. When a regulator
// opens a new mobile prefix, update the table in rules() and add a test row.
//
// E164.String and slog output are masked. Use Unmasked only where the full
// number is required (sending a message, the merchant's own API response).
package phone

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// Errors. Match with errors.Is.
var (
	ErrInvalid            = errors.New("phone: invalid mobile number")
	ErrUnsupportedCountry = errors.New("phone: unsupported country")
)

// Region is an ISO 3166-1 alpha-2 code.
type Region string

// Supported regions.
const (
	EG Region = "EG"
	SA Region = "SA"
	AE Region = "AE"
	KW Region = "KW"
	QA Region = "QA"
	BH Region = "BH"
	OM Region = "OM"
)

// maxInputLen bounds work on untrusted input.
const maxInputLen = 64

type rule struct {
	region   Region
	cc       string // country calling code, digits only
	trunk    bool   // national format has a leading 0
	length   int    // national significant number length
	prefixes []string
}

// rules is a function, not a package variable, so there is no global state.
func rules() []rule {
	return []rule{
		{region: EG, cc: "20", trunk: true, length: 10, prefixes: []string{"10", "11", "12", "15"}},
		{region: SA, cc: "966", trunk: true, length: 9, prefixes: []string{"50", "53", "54", "55", "56", "57", "58", "59"}},
		{region: AE, cc: "971", trunk: true, length: 9, prefixes: []string{"50", "52", "54", "55", "56", "58"}},
		{region: KW, cc: "965", length: 8, prefixes: []string{"4", "5", "6", "9"}},
		{region: QA, cc: "974", length: 8, prefixes: []string{"3", "5", "6", "7"}},
		{region: BH, cc: "973", length: 8, prefixes: []string{"3", "63", "66"}},
		{region: OM, cc: "968", length: 8, prefixes: []string{"7", "9"}},
	}
}

func ruleFor(r Region) (rule, bool) {
	for _, x := range rules() {
		if x.region == r {
			return x, true
		}
	}
	return rule{}, false
}

// Regions lists supported regions.
func Regions() []Region {
	rs := rules()
	out := make([]Region, len(rs))
	for i, r := range rs {
		out[i] = r.region
	}
	return out
}

// E164 is a validated mobile number such as +201012345678.
// The zero value is "no number"; check IsZero.
type E164 struct {
	number string // "+" + cc + national
	region Region
	ccLen  int
}

// Parse normalises raw user input to E.164. Input may contain spaces,
// dashes, dots, parentheses, bidi marks, Arabic-Indic digits, a leading
// "+" or "00", a national trunk "0", or the country code without "+".
// Without a country code the number is read in defaultRegion.
func Parse(raw string, defaultRegion Region) (E164, error) {
	if len(raw) > maxInputLen {
		return E164{}, fmt.Errorf("%w: too long", ErrInvalid)
	}
	digits, international, err := normalize(raw)
	if err != nil {
		return E164{}, err
	}
	if international {
		for _, r := range rules() {
			if strings.HasPrefix(digits, r.cc) {
				return r.national(digits[len(r.cc):])
			}
		}
		return E164{}, ErrUnsupportedCountry
	}
	r, ok := ruleFor(defaultRegion)
	if !ok {
		return E164{}, fmt.Errorf("%w: default region %q", ErrUnsupportedCountry, string(defaultRegion))
	}
	if n, err := r.national(digits); err == nil {
		return n, nil
	}
	// Country code typed without "+" or "00", e.g. "201012345678".
	if strings.HasPrefix(digits, r.cc) {
		return r.national(digits[len(r.cc):])
	}
	return E164{}, fmt.Errorf("%w: not a %s mobile", ErrInvalid, r.region)
}

// national validates a national significant number (optionally with the
// trunk 0, which people often keep after the country code).
func (r rule) national(n string) (E164, error) {
	if r.trunk && len(n) == r.length+1 && n[0] == '0' {
		n = n[1:]
	}
	if len(n) != r.length {
		return E164{}, fmt.Errorf("%w: %s mobiles have %d digits", ErrInvalid, r.region, r.length)
	}
	for _, p := range r.prefixes {
		if strings.HasPrefix(n, p) {
			return E164{number: "+" + r.cc + n, region: r.region, ccLen: len(r.cc)}, nil
		}
	}
	return E164{}, fmt.Errorf("%w: unknown %s mobile prefix", ErrInvalid, r.region)
}

// normalize strips formatting and maps digits to ASCII. It reports whether
// the number was written in international form ("+" or "00").
func normalize(raw string) (digits string, international bool, err error) {
	var b strings.Builder
	b.Grow(len(raw))
	seenDigit, seenPlus := false, false
	for _, c := range raw {
		switch {
		case c >= '0' && c <= '9':
			b.WriteRune(c)
			seenDigit = true
		case c >= '٠' && c <= '٩':
			b.WriteRune('0' + (c - '٠'))
			seenDigit = true
		case c >= '۰' && c <= '۹':
			b.WriteRune('0' + (c - '۰'))
			seenDigit = true
		case c == '+':
			if seenDigit || seenPlus {
				return "", false, fmt.Errorf("%w: misplaced '+'", ErrInvalid)
			}
			seenPlus = true
		case isSeparator(c):
		default:
			return "", false, fmt.Errorf("%w: unexpected character %q", ErrInvalid, c)
		}
	}
	digits = b.String()
	if digits == "" {
		return "", false, fmt.Errorf("%w: empty", ErrInvalid)
	}
	if seenPlus {
		return digits, true, nil
	}
	if strings.HasPrefix(digits, "00") {
		return digits[2:], true, nil
	}
	return digits, false, nil
}

func isSeparator(c rune) bool {
	switch c {
	case ' ', '\t', '-', '.', '(', ')', '/',
		'\u00a0',                     // no-break space
		'\u200e', '\u200f', '\u061c', // LRM, RLM, ALM
		'\u202a', '\u202b', '\u202c', '\u202d', '\u202e', // embeddings/overrides
		'\u2066', '\u2067', '\u2068', '\u2069': // isolates
		return true
	default:
		return false
	}
}

// IsZero reports whether e is the zero (absent) number.
func (e E164) IsZero() bool { return e.number == "" }

// Region returns the number's country.
func (e E164) Region() Region { return e.region }

// Unmasked returns the full E.164 string, e.g. "+201012345678".
// Never log it (CLAUDE.md security rules).
func (e E164) Unmasked() string { return e.number }

// Masked keeps the country code, the first two and the last two national
// digits: "+2010******78".
func (e E164) Masked() string {
	if e.IsZero() {
		return ""
	}
	head := 1 + e.ccLen + 2
	tail := len(e.number) - 2
	return e.number[:head] + strings.Repeat("*", tail-head) + e.number[tail:]
}

// String returns the masked form so fmt and %v never leak the number.
func (e E164) String() string { return e.Masked() }

// LogValue implements slog.LogValuer: numbers are always masked in logs.
func (e E164) LogValue() slog.Value { return slog.StringValue(e.Masked()) }

// MarshalText returns the full number (for authorized API responses).
func (e E164) MarshalText() ([]byte, error) { return []byte(e.number), nil }

// UnmarshalText accepts only canonical E.164 ("+" and digits).
func (e *E164) UnmarshalText(b []byte) error {
	v, err := ParseE164(string(b))
	if err != nil {
		return err
	}
	*e = v
	return nil
}

// ParseE164 accepts only the canonical "+<cc><national>" form, as stored.
func ParseE164(s string) (E164, error) {
	if len(s) < 2 || s[0] != '+' || len(s) > 16 {
		return E164{}, fmt.Errorf("%w: not canonical E.164", ErrInvalid)
	}
	for _, c := range s[1:] {
		if c < '0' || c > '9' {
			return E164{}, fmt.Errorf("%w: not canonical E.164", ErrInvalid)
		}
	}
	return Parse(s, "")
}

// Value implements driver.Valuer (stored as text; NULL when zero).
func (e E164) Value() (driver.Value, error) {
	if e.IsZero() {
		return nil, nil
	}
	return e.number, nil
}

// Scan implements sql.Scanner.
func (e *E164) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*e = E164{}
		return nil
	case string:
		return e.UnmarshalText([]byte(v))
	case []byte:
		return e.UnmarshalText(v)
	default:
		return fmt.Errorf("%w: cannot scan %T", ErrInvalid, src)
	}
}
