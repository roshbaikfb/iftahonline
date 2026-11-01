// Package domain holds the orders module's rules: the order state
// machine, what a new order must contain, and the events orders publish.
// It imports only the standard library and internal/platform (CLAUDE.md
// rule 3).
package domain

import (
	"fmt"

	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// Status is an order's state (ADR-0021).
type Status string

// Statuses.
const (
	StatusPending        Status = "pending"         // placed, waiting for the merchant to confirm (by phone)
	StatusConfirmed      Status = "confirmed"       // the customer confirmed
	StatusReadyToShip    Status = "ready_to_ship"   // packed, waiting for the courier
	StatusShipped        Status = "shipped"         // with the courier; stock has left
	StatusDelivered      Status = "delivered"       // delivered and paid (cash on delivery)
	StatusCancelled      Status = "cancelled"       // cancelled before it shipped
	StatusFailedDelivery Status = "failed_delivery" // the courier could not deliver
	StatusReturned       Status = "returned"        // the goods came back; stock restocked
)

// Statuses lists every status in lifecycle order.
func Statuses() []Status {
	return []Status{StatusPending, StatusConfirmed, StatusReadyToShip, StatusShipped, StatusDelivered,
		StatusCancelled, StatusFailedDelivery, StatusReturned}
}

// ParseStatus validates a status from external input.
func ParseStatus(s string) (Status, error) {
	for _, st := range Statuses() {
		if string(st) == s {
			return st, nil
		}
	}
	return "", errs.Validation(errs.FieldError{Field: "status", Code: "invalid", Message: fmt.Sprintf("unknown status %q", s)})
}

// StockEffect is what a transition does to the order's held stock
// (ADR-0020): the catalog's ReleaseStockTx, CommitStockTx or RestockTx.
type StockEffect int

// Stock effects.
const (
	StockNone    StockEffect = iota
	StockRelease             // cancelled before shipping: the hold is given back
	StockCommit              // shipped: the units leave on_hand
	StockRestock             // returned: the units come back on hand
)

// transition is one allowed edge of the state machine.
type transition struct {
	stock StockEffect
	perm  authz.Permission
}

// transitions is THE state machine: every allowed edge, with its stock
// effect and the permission it needs. Anything absent is forbidden.
//
//   - Confirming and cancelling are customer-facing decisions
//     (orders.manage); moving goods is fulfilment (orders.fulfil).
//   - A failed delivery can be retried (back to shipped; the stock is
//     already committed, so committing again is a no-op) or returned.
//   - cancelled and returned are final.
func transitions() map[Status]map[Status]transition {
	manage := func(s StockEffect) transition { return transition{stock: s, perm: authz.PermOrdersManage} }
	fulfil := func(s StockEffect) transition { return transition{stock: s, perm: authz.PermOrdersFulfil} }
	return map[Status]map[Status]transition{
		StatusPending:        {StatusConfirmed: manage(StockNone), StatusCancelled: manage(StockRelease)},
		StatusConfirmed:      {StatusReadyToShip: fulfil(StockNone), StatusCancelled: manage(StockRelease)},
		StatusReadyToShip:    {StatusShipped: fulfil(StockCommit), StatusCancelled: manage(StockRelease)},
		StatusShipped:        {StatusDelivered: fulfil(StockNone), StatusFailedDelivery: fulfil(StockNone)},
		StatusFailedDelivery: {StatusShipped: fulfil(StockCommit), StatusReturned: fulfil(StockRestock)},
		StatusDelivered:      {StatusReturned: fulfil(StockRestock)},
	}
}

// ErrInvalidTransition matches every *TransitionError with errors.Is.
var ErrInvalidTransition = errs.Conflict("invalid_transition", "the order cannot move to that status from its current one")

// TransitionError is a forbidden move, naming both ends.
type TransitionError struct {
	From, To Status
}

func (e *TransitionError) Error() string {
	return fmt.Sprintf("orders: cannot move an order from %s to %s", e.From, e.To)
}

// Unwrap returns ErrInvalidTransition, so the problem renderer maps it
// to 409 invalid_transition.
func (e *TransitionError) Unwrap() error { return ErrInvalidTransition }

// Step is an allowed transition: its stock effect and the permission it
// needs.
type Step struct {
	From, To   Status
	Stock      StockEffect
	Permission authz.Permission
}

// Transition returns the step from -> to, or a *TransitionError.
func Transition(from, to Status) (Step, error) {
	t, ok := transitions()[from][to]
	if !ok {
		return Step{}, &TransitionError{From: from, To: to}
	}
	return Step{From: from, To: to, Stock: t.stock, Permission: t.perm}, nil
}

// Final reports whether no transition leaves s.
func (s Status) Final() bool { return len(transitions()[s]) == 0 }

// CountsAsSale reports whether an order in status s counts towards the
// store's bestsellers (ADR-0033): from its confirmation until it is
// cancelled, fails delivery or comes back.
func CountsAsSale(s Status) bool {
	switch s {
	case StatusConfirmed, StatusReadyToShip, StatusShipped, StatusDelivered:
		return true
	}
	return false
}

// OutcomeReason is why an order did not reach its customer (ADR-0065):
// what the risk module learns a phone's behaviour from. It belongs to the
// order's latest move, so a later move clears it.
type OutcomeReason string

// Outcome reasons.
const (
	ReasonNone        OutcomeReason = ""
	ReasonRefused     OutcomeReason = "refused"     // the customer refused the parcel or the order
	ReasonNoAnswer    OutcomeReason = "no_answer"   // the customer did not answer the courier or the store
	ReasonUnconfirmed OutcomeReason = "unconfirmed" // the customer did not answer the confirmation message (system only)
)

// ParseReason validates a person's reason for a move to status to: only
// a cancellation, a failed delivery or a return takes one, and only
// refused or no_answer (unconfirmed is the confirmation's own).
func ParseReason(s string, to Status) (OutcomeReason, error) {
	r := OutcomeReason(s)
	switch r {
	case ReasonNone:
		return r, nil
	case ReasonRefused, ReasonNoAnswer:
		if TakesReason(to) {
			return r, nil
		}
		return "", errs.Validation(errs.FieldError{Field: "reason", Code: "not_allowed",
			Message: "only a cancellation, a failed delivery or a return takes a reason"})
	case ReasonUnconfirmed:
	}
	return "", errs.Validation(errs.FieldError{Field: "reason", Code: "invalid", Message: fmt.Sprintf("unknown reason %q", s)})
}

// TakesReason reports whether a move to s may carry an outcome reason.
func TakesReason(s Status) bool {
	return s == StatusCancelled || s == StatusFailedDelivery || s == StatusReturned
}
