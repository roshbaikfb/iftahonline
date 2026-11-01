package domain

import "github.com/roshbaikfb/iftahonline/internal/platform/errs"

// RiskAction is what the store's risk rules did to an order when it was
// placed (ADR-0066).
type RiskAction string

// Risk actions.
const (
	RiskNone     RiskAction = ""
	RiskHold     RiskAction = "hold"     // waits for the merchant's review; cannot be confirmed until released
	RiskVerify   RiskAction = "verify"   // the customer's own "yes" does not confirm it: the merchant calls
	RiskReleased RiskAction = "released" // a hold the merchant lifted
	RiskPrepay   RiskAction = "prepay"   // the shopper pays the delivery upfront (ADR-0067)
)

// Risk is the verdict the risk rules give a new storefront order: the
// customer's band then, and the action ("" for none).
type Risk struct {
	Band   string
	Action RiskAction
}

// Errors of held orders.
var (
	// ErrOrderHeld is a confirmation of an order the rules hold.
	ErrOrderHeld = errs.Conflict("order_held", "the order is held for review: release it before confirming")
	// ErrNotHeld is a release of an order that is not held (or no longer
	// pending).
	ErrNotHeld = errs.Conflict("order_not_held", "the order is not held")
	// ErrNeedsCall is the customer's own confirmation of an order the
	// rules want the merchant to verify by phone.
	ErrNeedsCall = errs.Conflict("order_needs_call", "the store confirms this order by phone")
	// ErrDepositDue is the customer's own confirmation of an order whose
	// prepaid delivery is not paid yet: paying confirms it (ADR-0070).
	ErrDepositDue = errs.Conflict("order_deposit_due", "the order is confirmed once its delivery is paid")
)
