// Package domain holds the setup wizard's rules (ADR-0034): the
// activities and looks a starter template is picked by, and the state
// of a store's wizard.
package domain

import (
	"slices"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// Activity is what the store sells; it picks the starter template.
type Activity string

// The activities.
const (
	Clothing    Activity = "clothing"
	Electronics Activity = "electronics"
	Beauty      Activity = "beauty"
	Perfume     Activity = "perfume"
	Kids        Activity = "kids"
	Food        Activity = "food"
	Home        Activity = "home"
	Other       Activity = "other"
)

// Activities lists every activity, in the wizard's order.
func Activities() []Activity {
	return []Activity{Clothing, Electronics, Beauty, Perfume, Kids, Food, Home, Other}
}

// Look is the storefront's layout; the colour is chosen apart.
type Look string

// The looks.
const (
	Classic Look = "classic"
	Modern  Look = "modern"
	Soft    Look = "soft"
)

// Looks lists every look, in the wizard's order.
func Looks() []Look { return []Look{Classic, Modern, Soft} }

// Choice is the merchant's pick in the wizard.
type Choice struct {
	Activity Activity
	Look     Look
	Color    string // #rrggbb, lower case once validated
}

// ValidateChoice normalizes c and reports every invalid field at once.
func ValidateChoice(c Choice) (Choice, error) {
	out := Choice{Activity: c.Activity, Look: c.Look, Color: strings.ToLower(strings.TrimSpace(c.Color))}
	var fields []errs.FieldError
	if !slices.Contains(Activities(), out.Activity) {
		fields = append(fields, errs.FieldError{Field: "activity", Code: "invalid", Message: "choose one of the listed activities"})
	}
	if !slices.Contains(Looks(), out.Look) {
		fields = append(fields, errs.FieldError{Field: "look", Code: "invalid", Message: "choose one of the listed looks"})
	}
	if !hexColor(out.Color) {
		fields = append(fields, errs.FieldError{Field: "color", Code: "invalid", Message: "color must be #rrggbb"})
	}
	if len(fields) > 0 {
		return Choice{}, errs.Validation(fields...)
	}
	return out, nil
}

func hexColor(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, r := range s[1:] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// Status says whether the wizard is still open.
type Status string

// The statuses.
const (
	InProgress Status = "in_progress"
	Completed  Status = "completed"
)

// TemplateStatus is where applying the chosen template stands.
type TemplateStatus string

// The template statuses.
const (
	TemplateNone      TemplateStatus = "none"
	TemplatePreparing TemplateStatus = "preparing"
	TemplateReady     TemplateStatus = "ready"
	TemplateFailed    TemplateStatus = "failed"
)

// State is a store's wizard. A store made before the wizard has none:
// the zero State, which reads as completed.
type State struct {
	Started bool
	Choice  *Choice
	// Template is the apply job's status; Seq counts the choices, so a
	// job can tell whether its choice is still the current one.
	Template        TemplateStatus
	TemplateVersion int
	Seq             int
	CompletedAt     *time.Time
}

// Status reports whether the wizard is open.
func (s State) Status() Status {
	if !s.Started || s.CompletedAt != nil {
		return Completed
	}
	return InProgress
}

// ErrClosed refuses a template choice once the wizard is closed.
var ErrClosed = errs.Conflict("onboarding_completed", "the setup wizard is closed")
