// Package gateway is the contract every payment gateway adapter meets
// (ADR-0029): create a payment on the gateway's hosted page, verify its
// webhooks, fetch a payment's status, and refund it. The fake adapter
// (gateway/fake) and Paymob (gateway/paymob) pass one shared contract
// suite (gateway/gatewaytest).
//
// No card data ever reaches us: the shopper types it on the gateway's
// page. Adapters are stateless; the merchant's credentials come with
// each call.
package gateway

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/money"
)

// Errors. Match with errors.Is.
var (
	// ErrAuth: the gateway refused the credentials.
	ErrAuth = errors.New("gateway: credentials refused")
	// ErrRejected: the gateway refused the request itself.
	ErrRejected = errors.New("gateway: request rejected")
	// ErrForged: a webhook whose signature does not verify.
	ErrForged = errors.New("gateway: webhook signature does not verify")
	// ErrBadWebhook: a webhook body that is not the gateway's.
	ErrBadWebhook = errors.New("gateway: unreadable webhook")
)

// State is where a payment is at the gateway.
type State string

// States.
const (
	StatePending State = "pending" // created; the shopper has not paid (yet)
	StatePaid    State = "paid"    // captured
	StateFailed  State = "failed"  // declined or abandoned at the gateway
)

// Credentials are the merchant's account at the gateway. They print and
// log as redacted.
type Credentials struct {
	SecretKey      string `json:"secret_key"`
	PublicKey      string `json:"public_key"`
	WebhookSecret  string `json:"webhook_secret"`  // Paymob: the HMAC secret
	APIKey         string `json:"api_key"`         // Paymob: for transaction inquiries
	IntegrationIDs []int  `json:"integration_ids"` // Paymob: the card integrations offered
}

// String implements fmt.Stringer.
func (Credentials) String() string { return "gateway.Credentials{redacted}" }

// GoString implements fmt.GoStringer.
func (Credentials) GoString() string { return "gateway.Credentials{redacted}" }

// LogValue implements slog.LogValuer.
func (Credentials) LogValue() slog.Value { return slog.StringValue("redacted") }

// PaymentRequest asks for a hosted payment page.
type PaymentRequest struct {
	// Reference is our payment ID: the gateway returns it with every
	// transaction, and status inquiries use it.
	Reference string
	Amount    money.Money
	Customer  Customer
	// Description names what is paid, e.g. "Order 7P0R1S2T".
	Description string
	// NotificationURL receives the gateway's webhooks; RedirectURL is
	// where the shopper returns after paying.
	NotificationURL string
	RedirectURL     string
}

// Customer is who pays.
type Customer struct {
	FirstName, LastName string
	Phone               string // E.164
	Email               string
}

// Checkout is a created payment: where to send the shopper.
type Checkout struct {
	URL         string
	ProviderRef string // the gateway's own payment (intention) ID
}

// Status is a payment's state at the gateway.
type Status struct {
	State         State
	Amount        money.Money // captured (zero until paid)
	Refunded      money.Money // refunded so far
	TransactionID string      // the gateway's transaction, for refunds
	At            time.Time
}

// WebhookEvent is what a verified webhook says. It is a hint: the caller
// fetches the status before changing anything (ADR-0024).
type WebhookEvent struct {
	EventID   string // stable across retries of one change
	Reference string // our payment ID
}

// WebhookRequest is a delivery as received.
type WebhookRequest struct {
	Header http.Header
	Query  url.Values
	Body   []byte
}

// IntegrationIDsNeeded is implemented by gateways whose accounts route
// card payments through integration IDs the merchant must give (Paymob).
type IntegrationIDsNeeded interface {
	NeedsIntegrationIDs() bool
}

// Gateway is one payment gateway's API.
type Gateway interface {
	// Name is the gateway's ID in connections and webhook URLs.
	Name() string
	// Verify checks creds with a cheap authenticated call.
	Verify(ctx context.Context, creds Credentials) error
	// CreatePayment creates a payment on the gateway's hosted page.
	CreatePayment(ctx context.Context, creds Credentials, req PaymentRequest) (Checkout, error)
	// VerifyWebhook checks a delivery's signature with creds and reads
	// it: ErrForged or ErrBadWebhook.
	VerifyWebhook(creds Credentials, req WebhookRequest) (WebhookEvent, error)
	// ParseWebhook reads an already verified body (a stored receipt's):
	// ErrBadWebhook when it is not the gateway's.
	ParseWebhook(body []byte) (WebhookEvent, error)
	// FetchStatus returns a payment's state by our reference; a payment
	// the gateway has no transaction for yet is pending.
	FetchStatus(ctx context.Context, creds Credentials, reference string, cur money.Currency) (Status, error)
	// Refund refunds amount of a paid transaction; it returns the
	// gateway's refund ID.
	Refund(ctx context.Context, creds Credentials, transactionID string, amount money.Money) (string, error)
}
