package app

import (
	"context"
	"errors"
	"net/url"
)

// Checkout errors.
var (
	// ErrNoCheckout: the invoice's currency has no checkout configured.
	ErrNoCheckout = errors.New("billing: no checkout for this currency")
	// ErrForgedCallback: a callback not signed by the platform's account.
	ErrForgedCallback = errors.New("billing: callback not signed")
	// ErrBadCallback: a signed callback that cannot be read.
	ErrBadCallback = errors.New("billing: unreadable callback")
)

// CheckoutRequest opens the gateway's page for an invoice.
type CheckoutRequest struct {
	Reference       string // ours: the checkout's
	AmountMinor     int64
	Currency        string
	Description     string
	Name, Email     string // who pays, as the gateway asks
	NotificationURL string // the platform's callback URL
	RedirectURL     string // where the merchant comes back
}

// CheckoutPage is where the merchant pays.
type CheckoutPage struct {
	URL             string
	ProviderOrderID string // the gateway's order, when it says (card token callbacks name it)
}

// CallbackKind is what a gateway callback is about.
type CallbackKind string

// Callback kinds.
const (
	CallbackTransaction CallbackKind = "transaction" // a checkout's payment
	CallbackToken       CallbackKind = "token"       // a card saved on a checkout
)

// Callback is a verified gateway callback.
type Callback struct {
	Kind            CallbackKind
	EventID         string // one change of one object
	Reference       string // our checkout's (transactions)
	ProviderOrderID string // the gateway's order (both)
	TransactionID   string
	Token           string // the saved card (tokens)
	Label           string // what the merchant sees of it
}

// CheckoutGateway is a Gateway that also takes payments on its own page
// and saves the card there (ADR-0053).
type CheckoutGateway interface {
	Gateway
	Checkout(ctx context.Context, c CheckoutRequest) (CheckoutPage, error)
	// ParseCallback verifies a callback's signature and reads it.
	ParseCallback(query url.Values, body []byte) (Callback, error)
	// ReadCallback reads a callback verified when it was received.
	ReadCallback(body []byte) (Callback, error)
	// Paid asks the gateway whether reference is paid in full: a
	// callback is a hint, the gateway's answer the evidence.
	Paid(ctx context.Context, currency, reference string, amountMinor int64) (transactionID string, paid bool, err error)
}
