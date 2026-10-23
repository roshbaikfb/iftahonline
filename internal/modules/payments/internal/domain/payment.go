// Package domain holds the payments rules (ADR-0029): a payment's
// statuses, how the gateway's word moves it, and what can be refunded.
package domain

import (
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// ErrNotFound is returned by the store when no row matches.
var ErrNotFound = errors.New("payments: not found")

// Status is where a payment is.
type Status string

// Statuses.
const (
	StatusPending           Status = "pending"
	StatusPaid              Status = "paid"
	StatusFailed            Status = "failed"
	StatusExpired           Status = "expired"
	StatusPartiallyRefunded Status = "partially_refunded"
	StatusRefunded          Status = "refunded"
)

// Captured reports whether the payment took the shopper's money.
func (s Status) Captured() bool {
	return s == StatusPaid || s == StatusPartiallyRefunded || s == StatusRefunded
}

// Open reports whether the payment may still be paid.
func (s Status) Open() bool { return s == StatusPending }

// Payment is one attempt to pay an order on the gateway's page.
type Payment struct {
	ID            ulid.ID
	OrderID       ulid.ID
	ConnectionID  ulid.ID
	Gateway       string
	Amount        money.Money
	Refunded      money.Money
	Status        Status
	CheckoutURL   string
	ProviderRef   string
	TransactionID string
	Failure       string
	StatusAt      time.Time
	CreatedAt     time.Time
	Version       int
}

// GatewayWord is what the gateway says of a payment (gateway.Status in
// domain terms).
type GatewayWord struct {
	Paid          bool
	Failed        bool
	Amount        money.Money // captured
	Refunded      money.Money
	TransactionID string
}

// Next is where the gateway's word moves a payment: the new status,
// refunded amount and transaction, and a failure note when the word
// cannot be taken (a capture of another amount than asked, which is held
// for a person to look at). A captured payment never goes back to open.
func Next(p Payment, w GatewayWord) (st Status, refunded money.Money, txn, failure string) {
	st, refunded, txn, failure = p.Status, p.Refunded, p.TransactionID, p.Failure
	if w.TransactionID != "" {
		txn = w.TransactionID
	}
	switch {
	case w.Paid && w.Amount.Amount() != p.Amount.Amount():
		return st, refunded, txn, "the gateway captured " + w.Amount.String() + " for " + p.Amount.String()
	case w.Paid:
		refunded = w.Refunded
		switch {
		case refunded.Amount() >= p.Amount.Amount():
			refunded, st = p.Amount, StatusRefunded
		case refunded.Amount() > 0:
			st = StatusPartiallyRefunded
		default:
			st = StatusPaid
		}
		return st, refunded, txn, ""
	case w.Failed && st.Open():
		return StatusFailed, refunded, txn, failure
	}
	return st, refunded, txn, failure
}

// Refundable is what can still be refunded of p, with requested (asked
// for, not yet answered) refunds already taken off.
func Refundable(p Payment, requested money.Money) (money.Money, error) {
	if !p.Status.Captured() {
		return money.Zero(p.Amount.Currency())
	}
	left, err := p.Amount.Sub(p.Refunded)
	if err != nil {
		return money.Money{}, err
	}
	if left, err = left.Sub(requested); err != nil {
		return money.Money{}, err
	}
	if left.IsNegative() {
		return money.Zero(p.Amount.Currency())
	}
	return left, nil
}

// Refund statuses.
const (
	RefundRequested = "requested"
	RefundSucceeded = "succeeded"
	RefundFailed    = "failed"
)

// Refund is a refund asked of a payment.
type Refund struct {
	ID         ulid.ID
	PaymentID  ulid.ID
	Amount     money.Money
	Status     string
	Reason     string
	ProviderID string
	Failure    string
	CreatedAt  time.Time
}

// MaxReasonLen bounds a refund's reason.
const MaxReasonLen = 300

// CleanReason trims a refund reason and checks its length.
func CleanReason(r string) (string, error) {
	r = strings.TrimSpace(r)
	if len([]rune(r)) > MaxReasonLen {
		return "", errs.Validation(errs.FieldError{Field: "reason", Code: "too_long", Message: "at most 300 characters"})
	}
	return r, nil
}

// Connection statuses.
const (
	ConnectionActive       = "active"
	ConnectionDisconnected = "disconnected"
)

// Connection is a store's account at a gateway; its credentials stay
// sealed.
type Connection struct {
	ID             ulid.ID
	Gateway        string
	Status         string
	CreatedAt      time.Time
	DisconnectedAt *time.Time
}

// CleanKey trims a pasted key and checks it is one printable token.
func CleanKey(field, k string, required bool) (string, error) {
	k = strings.TrimSpace(k)
	if k == "" && !required {
		return "", nil
	}
	if k == "" || len(k) > 512 || strings.IndexFunc(k, func(r rune) bool { return !unicode.IsPrint(r) || unicode.IsSpace(r) }) >= 0 {
		return "", errs.Validation(errs.FieldError{Field: field, Code: "invalid", Message: "paste the key from your gateway account"})
	}
	return k, nil
}
