package domain

import (
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// Payment rules and taxes by the merchant's country (ADR-0089, approved
// 2026-11-05): the console sets them; billing and the storefront read
// them.

// OtherCountries is the row every country without its own falls back to
// (CLDR's unknown region).
const OtherCountries = "ZZ"

// Gateways are the adapters the platform has: a rule may only name these.
func Gateways() []string { return []string{"paymob", "stripe"} }

var countryRE = regexp.MustCompile(`^[A-Z]{2}$`)

// PaymentRule is how a country's merchants are paid: the gateway the
// platform bills them through, and those their storefronts may take
// cards with (cash on delivery always).
type PaymentRule struct {
	Country        string
	BillingGateway string
	StoreGateways  []string
}

// Validate checks a rule the console saves.
func (r PaymentRule) Validate() error {
	var fe []errs.FieldError
	if !countryRE.MatchString(r.Country) {
		fe = append(fe, errs.FieldError{Field: "country", Code: "invalid", Message: "an ISO 3166-1 alpha-2 country, or ZZ for the others"})
	}
	if !slices.Contains(Gateways(), r.BillingGateway) {
		fe = append(fe, errs.FieldError{Field: "billing_gateway", Code: "invalid", Message: "paymob or stripe"})
	}
	seen := map[string]bool{}
	for _, g := range r.StoreGateways {
		if !slices.Contains(Gateways(), g) || seen[g] {
			fe = append(fe, errs.FieldError{Field: "store_gateways", Code: "invalid", Message: "paymob or stripe, each once"})
			break
		}
		seen[g] = true
	}
	if len(fe) > 0 {
		return errs.Validation(fe...)
	}
	return nil
}

// RuleFor is the rule of country among rules: its own, else the others'.
func RuleFor(rules []PaymentRule, country string) (PaymentRule, bool) {
	var other *PaymentRule
	for i := range rules {
		switch rules[i].Country {
		case country:
			return rules[i], true
		case OtherCountries:
			other = &rules[i]
		}
	}
	if other == nil {
		return PaymentRule{}, false
	}
	r := *other
	r.Country = country
	return r, true
}

// Tax is a country's VAT on the platform's invoices.
type Tax struct {
	Country string
	RateBps int32
	NameAr  string
	NameEn  string
}

// Validate checks a tax the console saves: 0 to 50%.
func (t Tax) Validate() error {
	var fe []errs.FieldError
	if !countryRE.MatchString(t.Country) {
		fe = append(fe, errs.FieldError{Field: "country", Code: "invalid", Message: "an ISO 3166-1 alpha-2 country, or ZZ for the others"})
	}
	if t.RateBps < 0 || t.RateBps > 5000 {
		fe = append(fe, errs.FieldError{Field: "rate_bps", Code: "out_of_range", Message: "0 to 5000 (50%)"})
	}
	for field, name := range map[string]string{"name_ar": t.NameAr, "name_en": t.NameEn} {
		if n := utf8.RuneCountInString(strings.TrimSpace(name)); n < 1 || n > 60 {
			fe = append(fe, errs.FieldError{Field: field, Code: "length", Message: "1 to 60 characters"})
		}
	}
	if len(fe) > 0 {
		return errs.Validation(fe...)
	}
	return nil
}
