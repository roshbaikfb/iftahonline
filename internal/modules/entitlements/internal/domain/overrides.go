package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
)

// Override is what one store is granted of a feature instead of its
// plan's grant, set from the console (ADR-0057): above the plan (a
// feature for a promotion) or below it (a feature taken away).
type Override struct {
	Feature   Feature
	Grant     Grant
	ExpiresAt time.Time // zero: until cleared
	Reason    string
	UpdatedAt time.Time
}

// Active reports whether o applies at now.
func (o Override) Active(now time.Time) bool { return o.ExpiresAt.IsZero() || now.Before(o.ExpiresAt) }

// Validate checks an override the console sets at now: an enforced
// feature (a display-only key changes nothing), a flag of 0 or 1, a
// deadline in the future and a reason.
func (o Override) Validate(now time.Time) error {
	var fe []errs.FieldError
	kind := o.Feature.Kind()
	switch {
	case kind == "":
		fe = append(fe, errs.FieldError{Field: "feature", Code: "not_enforced", Message: fmt.Sprintf("%q is not a feature the code checks", o.Feature)})
	case o.Grant.Value < 0:
		fe = append(fe, errs.FieldError{Field: "value", Code: "invalid", Message: "0 or more"})
	case kind == Flag && (o.Grant.Unlimited || o.Grant.Value > 1):
		fe = append(fe, errs.FieldError{Field: "value", Code: "invalid", Message: "a flag is 0 or 1"})
	}
	if !o.ExpiresAt.IsZero() && !o.ExpiresAt.After(now) {
		fe = append(fe, errs.FieldError{Field: "expires_at", Code: "past", Message: "must be in the future"})
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(o.Reason)); n < 3 || n > 500 {
		fe = append(fe, errs.FieldError{Field: "reason", Code: "invalid", Message: "3 to 500 characters"})
	}
	if len(fe) > 0 {
		return errs.Validation(fe...)
	}
	return nil
}

// With is g with the overrides active at now in place of the plan's
// grants. g is not changed.
func (g Grants) With(overrides []Override, now time.Time) Grants {
	out := make(Grants, len(g)+len(overrides))
	for f, v := range g {
		out[f] = v
	}
	for _, o := range overrides {
		if o.Active(now) && o.Feature.Kind() != "" {
			out[o.Feature] = o.Grant
		}
	}
	return out
}

// EventGrantsChanged is published in the transaction that sets or
// clears a store's override: caches of what it may do follow, as they
// do billing.subscription.changed (ADR-0057).
const EventGrantsChanged = "entitlements.grants.changed"

// GrantsChanged is EventGrantsChanged's payload.
type GrantsChanged struct {
	event.Meta
	Feature Feature `json:"feature"`
}
