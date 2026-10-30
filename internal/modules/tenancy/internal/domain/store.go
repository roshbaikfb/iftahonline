// Package domain holds the tenancy rules: what a store is, which slugs and
// hosts are acceptable, and how custom domains are verified. It is pure:
// standard library and internal/platform only (CLAUDE.md rule 3).
package domain

import (
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

// ErrNotFound is returned by the store layer when a row is missing, which
// includes rows of other tenants (RLS hides them).
var ErrNotFound = errors.New("tenancy: not found")

// StoreStatus is a store's lifecycle state.
type StoreStatus string

// Store statuses. Only active stores serve their storefront.
const (
	StatusActive    StoreStatus = "active"
	StatusSuspended StoreStatus = "suspended" // by the platform (abuse, billing)
	StatusClosed    StoreStatus = "closed"    // by the owner
)

// StatusHeld is how host resolution reports an active store held for
// billing (ADR-0051): its storefront shows a maintenance page. It is
// never a store's own status, which ParseStoreStatus validates.
const StatusHeld StoreStatus = "held"

// ParseResolutionStatus validates a resolved status: a store's own, or
// held.
func ParseResolutionStatus(s string) (StoreStatus, bool) {
	if StoreStatus(s) == StatusHeld {
		return StatusHeld, true
	}
	return ParseStoreStatus(s)
}

// ParseStoreStatus validates a status.
func ParseStoreStatus(s string) (StoreStatus, bool) {
	st := StoreStatus(s)
	return st, slices.Contains([]StoreStatus{StatusActive, StatusSuspended, StatusClosed}, st)
}

// Plan is the subscription plan. Billing changes it, never the merchant.
type Plan string

// Plans.
const (
	PlanFree  Plan = "free"
	PlanBasic Plan = "basic"
	PlanPro   Plan = "pro"
)

// Locale is the store's default language.
type Locale string

// Locales.
const (
	LocaleAR Locale = "ar"
	LocaleEN Locale = "en"
)

// Timezones lists the zones we accept: the markets we serve, plus UTC.
// An allowlist keeps validation independent of the host's tzdata.
func Timezones() []string {
	return []string{
		"Africa/Cairo", "Asia/Riyadh", "Asia/Dubai", "Asia/Kuwait",
		"Asia/Qatar", "Asia/Bahrain", "Asia/Muscat", "UTC",
	}
}

// Store is a merchant's shop: the tenant.
type Store struct {
	ID        tenant.StoreID
	Slug      string
	Name      string
	Status    StoreStatus
	Plan      Plan
	Currency  money.Currency
	Locale    Locale
	Timezone  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewStore is the input for creating a store.
type NewStore struct {
	Slug     string
	Name     string
	Currency string
	Locale   string
	Timezone string
}

// StoreSettings is what the merchant can change.
type StoreSettings struct {
	Name     string
	Locale   string
	Timezone string
}

// ValidateNewStore normalizes and validates in, reporting every invalid
// field at once.
func ValidateNewStore(in NewStore) (NewStore, error) {
	var fields []errs.FieldError
	out := NewStore{
		Slug:     strings.ToLower(strings.TrimSpace(in.Slug)),
		Name:     strings.TrimSpace(in.Name),
		Currency: strings.ToUpper(strings.TrimSpace(in.Currency)),
		Locale:   strings.TrimSpace(in.Locale),
		Timezone: strings.TrimSpace(in.Timezone),
	}
	if fe, ok := checkSlug(out.Slug); !ok {
		fields = append(fields, fe)
	}
	if fe, ok := checkName(out.Name); !ok {
		fields = append(fields, fe)
	}
	if !slices.Contains(money.Currencies(), money.Currency(out.Currency)) {
		fields = append(fields, errs.FieldError{Field: "currency", Code: "unsupported", Message: "currency must be one of EGP, SAR, AED"})
	}
	fields = append(fields, checkLocaleTZ(out.Locale, out.Timezone)...)
	if len(fields) > 0 {
		return NewStore{}, errs.Validation(fields...)
	}
	return out, nil
}

// ValidateSettings normalizes and validates a settings update.
func ValidateSettings(in StoreSettings) (StoreSettings, error) {
	out := StoreSettings{
		Name:     strings.TrimSpace(in.Name),
		Locale:   strings.TrimSpace(in.Locale),
		Timezone: strings.TrimSpace(in.Timezone),
	}
	var fields []errs.FieldError
	if fe, ok := checkName(out.Name); !ok {
		fields = append(fields, fe)
	}
	fields = append(fields, checkLocaleTZ(out.Locale, out.Timezone)...)
	if len(fields) > 0 {
		return StoreSettings{}, errs.Validation(fields...)
	}
	return out, nil
}

func checkName(name string) (errs.FieldError, bool) {
	n := utf8.RuneCountInString(name)
	if n < 1 || n > 120 || !utf8.ValidString(name) {
		return errs.FieldError{Field: "name", Code: "length", Message: "name must be 1 to 120 characters"}, false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return errs.FieldError{Field: "name", Code: "invalid", Message: "name must not contain control characters"}, false
		}
	}
	return errs.FieldError{}, true
}

func checkLocaleTZ(locale, tz string) []errs.FieldError {
	var fields []errs.FieldError
	if l := Locale(locale); l != LocaleAR && l != LocaleEN {
		fields = append(fields, errs.FieldError{Field: "locale", Code: "unsupported", Message: "locale must be ar or en"})
	}
	if !slices.Contains(Timezones(), tz) {
		fields = append(fields, errs.FieldError{Field: "timezone", Code: "unsupported", Message: "timezone is not supported"})
	}
	return fields
}
