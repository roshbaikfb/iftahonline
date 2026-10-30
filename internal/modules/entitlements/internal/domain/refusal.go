package domain

import (
	"fmt"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
)

// PlanLimitCode is the problem code a merchant's request gets when the
// store's plan does not allow it; the dashboard offers an upgrade.
const PlanLimitCode = "plan_limit"

// LimitError refuses one more of the counted feature f: 403 with code
// plan_limit. It matches ErrLimitReached with errors.Is.
func LimitError(f Feature, g Grant) error {
	return &errs.Error{
		Kind: errs.KindForbidden, Code: PlanLimitCode, Cause: ErrLimitReached,
		Message: fmt.Sprintf("the store's plan allows %d %s; upgrade the plan to add more", g.Value, f),
	}
}

// NotInPlan refuses the flag feature f, which the store's plan does not
// grant: 403 with code plan_limit. It matches ErrLimitReached.
func NotInPlan(f Feature) error {
	return &errs.Error{
		Kind: errs.KindForbidden, Code: PlanLimitCode, Cause: ErrLimitReached,
		Message: fmt.Sprintf("the store's plan does not include %s; upgrade the plan to use it", f),
	}
}

// EventUsageCrossed is published when a counted month's usage crosses
// 80% or 100% of the plan's limit.
const EventUsageCrossed = "entitlements.usage.crossed"

// UsageCrossed tells the merchant a monthly limit is near or reached.
type UsageCrossed struct {
	event.Meta
	Feature   Feature   `json:"feature"`
	Threshold Threshold `json:"threshold"`
	Period    time.Time `json:"period"`
	Used      int64     `json:"used"`
	Limit     int64     `json:"limit"`
}
