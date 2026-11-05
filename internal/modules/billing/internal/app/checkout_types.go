package app

import (
	"context"
	"errors"
	"net/http"
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
	ProviderOrderID string // the gateway's order, when it says (Paymob's card token callbacks name it)
	Session         string // the gateway's checkout, when it is asked by it (Stripe's Checkout Session)
}

// PaidQuery asks a gateway about a checkout.
type PaidQuery struct {
	Currency    string
	Reference   string // ours
	Session     string // CheckoutPage.Session, when the gateway gave one
	AmountMinor int64
}

// Payment is the gateway's answer about a checkout.
type Payment struct {
	TransactionID string
	Paid          bool // in full, in its currency
	// Token and Label are the card the payment saved, when the gateway
	// says so in the answer (Stripe) rather than in a callback of its own
	// (Paymob): "" for none.
	Token, Label string
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
	// ParseCallback verifies a callback's signature (in its header or its
	// query) and reads it.
	ParseCallback(header http.Header, query url.Values, body []byte) (Callback, error)
	// ReadCallback reads a callback verified when it was received.
	ReadCallback(body []byte) (Callback, error)
	// Paid asks the gateway whether a checkout is paid in full: a
	// callback is a hint, the gateway's answer the evidence.
	Paid(ctx context.Context, q PaidQuery) (Payment, error)
}
