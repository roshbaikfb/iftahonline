// Package country is the merchant's country (ADR-0088): an ISO 3166-1
// alpha-2 code, which decides how the store and its subscription are
// paid (Paymob in Egypt, Stripe elsewhere).
package country

import (
	"strings"

	"golang.org/x/text/language"
)

// Egypt is the one country paid through Paymob (approved 2026-11-05).
const Egypt = "EG"

// Normalize is code upper-cased and trimmed; ok is false unless it is an
// ISO 3166-1 alpha-2 country (not a macro-region or a private code).
func Normalize(code string) (string, bool) {
	c := strings.ToUpper(strings.TrimSpace(code))
	if len(c) != 2 || c[0] < 'A' || c[0] > 'Z' || c[1] < 'A' || c[1] > 'Z' {
		return "", false
	}
	r, err := language.ParseRegion(c)
	if err != nil || !r.IsCountry() || r.String() != c {
		return "", false
	}
	return c, true
}
