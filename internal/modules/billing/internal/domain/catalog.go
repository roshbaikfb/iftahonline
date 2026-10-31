package domain

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
)

// FeatureKind is how a feature is granted.
type FeatureKind string

// Feature kinds.
const (
	FeatureFlag    FeatureKind = "flag"    // 0 or 1
	FeatureCount   FeatureKind = "count"   // how many a store may hold
	FeatureMonthly FeatureKind = "monthly" // units per calendar month
)

// Feature is an entry of the features catalog. The console may add keys
// of its own: they are shown on plans, and enforced only once a module
// asks for them (ADR-0045). A key's kind never changes.
type Feature struct {
	Key    string
	Kind   FeatureKind
	NameAr string
	NameEn string
	Sort   int32
}

var (
	featureKeyRE = regexp.MustCompile(`^[a-z][a-z_]{0,31}$`)
	planCodeRE   = regexp.MustCompile(`^[a-z][a-z_]{1,31}$`)
)

// MaxTrialDays bounds a plan's free trial.
const MaxTrialDays = 90

// Validate checks a catalog entry from the console.
func (f Feature) Validate() error {
	var fe []errs.FieldError
	if !featureKeyRE.MatchString(f.Key) {
		fe = append(fe, errs.FieldError{Field: "key", Code: "invalid", Message: "lowercase letters and _, at most 32"})
	}
	switch f.Kind {
	case FeatureFlag, FeatureCount, FeatureMonthly:
	default:
		fe = append(fe, errs.FieldError{Field: "kind", Code: "invalid", Message: "flag, count or monthly"})
	}
	fe = append(fe, checkName("name_ar", f.NameAr)...)
	fe = append(fe, checkName("name_en", f.NameEn)...)
	if len(fe) > 0 {
		return errs.Validation(fe...)
	}
	return nil
}

func checkName(field, v string) []errs.FieldError {
	if n := utf8.RuneCountInString(strings.TrimSpace(v)); n < 1 || n > 80 {
		return []errs.FieldError{{Field: field, Code: "invalid", Message: "1 to 80 characters"}}
	}
	return nil
}

// Price is a plan's price in one currency and cycle.
type Price struct {
	Currency    string
	Cycle       Cycle
	AmountMinor int64
}

// CurrencyFee is a pay-as-you-sell plan's fee rule in one currency.
type CurrencyFee struct {
	Currency string
	FeeRule
}

// PlanVersion is one version of a plan with everything it sells and
// grants: what the console lists and publishes.
type PlanVersion struct {
	Plan
	Prices []Price
	Grants Grants
	Fees   []CurrencyFee
}

// Validate checks a version the console is about to publish. Feature
// keys are checked against the catalog by the database.
func (v PlanVersion) Validate() error {
	var fe []errs.FieldError
	if !planCodeRE.MatchString(v.Code) {
		fe = append(fe, errs.FieldError{Field: "code", Code: "invalid", Message: "lowercase letters and _, 2 to 32"})
	}
	fe = append(fe, checkName("name_ar", v.NameAr)...)
	fe = append(fe, checkName("name_en", v.NameEn)...)
	// What a plan sells depends on its kind: check it only for a known one.
	if _, err := ParseKind(string(v.Kind)); err != nil {
		fe = append(fe, errs.FieldError{Field: "kind", Code: "invalid", Message: "free, pays, subscription or agency"})
	} else {
		if v.TrialDays < 0 || v.TrialDays > MaxTrialDays || v.TrialDays > 0 && !v.Kind.Paid() {
			fe = append(fe, errs.FieldError{Field: "trial_days", Code: "invalid", Message: "0 to 90, and only on a paid plan"})
		}
		fe = append(fe, v.checkPrices()...)
		fe = append(fe, v.checkFees()...)
	}
	for key, g := range v.Grants {
		if !featureKeyRE.MatchString(key) || g.Value < 0 {
			fe = append(fe, errs.FieldError{Field: "grants." + key, Code: "invalid", Message: "a catalog key and a value of 0 or more"})
		}
	}
	if len(fe) > 0 {
		slices.SortFunc(fe, func(a, b errs.FieldError) int { return strings.Compare(a.Field, b.Field) })
		return errs.Validation(fe...)
	}
	return nil
}

// checkPrices: a paid plan sells a monthly price in each currency (an
// annual one is optional); other plans sell nothing.
func (v PlanVersion) checkPrices() []errs.FieldError {
	var fe []errs.FieldError
	seen := map[string]bool{}
	for i, p := range v.Prices {
		field := fmt.Sprintf("prices[%d]", i)
		if !money.Currency(p.Currency).Valid() || (p.Cycle != Monthly && p.Cycle != Annual) || p.AmountMinor < 0 {
			fe = append(fe, errs.FieldError{Field: field, Code: "invalid", Message: "a supported currency, monthly or annual, and an amount of 0 or more"})
			continue
		}
		key := p.Currency + "/" + string(p.Cycle)
		if seen[key] {
			fe = append(fe, errs.FieldError{Field: field, Code: "duplicate", Message: "one price per currency and cycle"})
		}
		seen[key] = true
	}
	switch {
	case v.Kind.Paid():
		for _, c := range money.Currencies() {
			if !seen[string(c)+"/"+string(Monthly)] {
				fe = append(fe, errs.FieldError{Field: "prices", Code: "missing", Message: "a monthly price in " + string(c)})
			}
		}
	case v.Kind == KindPays:
		// Pay as you sell has no period price; it may list zero prices.
		for i, p := range v.Prices {
			if p.AmountMinor != 0 {
				fe = append(fe, errs.FieldError{Field: fmt.Sprintf("prices[%d]", i), Code: "invalid", Message: "pay as you sell has no price"})
			}
		}
	default:
		if len(v.Prices) > 0 {
			fe = append(fe, errs.FieldError{Field: "prices", Code: "invalid", Message: "the free plan has no price"})
		}
	}
	return fe
}

// checkFees: pay as you sell has a fee rule in each currency; no other
// kind has one.
func (v PlanVersion) checkFees() []errs.FieldError {
	if v.Kind != KindPays {
		if len(v.Fees) > 0 {
			return []errs.FieldError{{Field: "fees", Code: "invalid", Message: "only pay as you sell has fees"}}
		}
		return nil
	}
	var fe []errs.FieldError
	seen := map[string]bool{}
	for i, f := range v.Fees {
		field := fmt.Sprintf("fees[%d]", i)
		if !money.Currency(f.Currency).Valid() || f.RateBps < 0 || f.RateBps > 10000 || f.MinMinor < 0 ||
			f.MaxMinor != nil && *f.MaxMinor < f.MinMinor || seen[f.Currency] {
			fe = append(fe, errs.FieldError{Field: field, Code: "invalid", Message: "one per currency; a rate of 0 to 10000 bps; 0 <= min <= max"})
		}
		seen[f.Currency] = true
	}
	for _, c := range money.Currencies() {
		if !seen[string(c)] {
			fe = append(fe, errs.FieldError{Field: "fees", Code: "missing", Message: "a fee in " + string(c)})
		}
	}
	return fe
}
