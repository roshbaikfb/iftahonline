// Package channel is the contract every messaging channel adapter meets
// (ADR-0039): send a message, verify and read the channel's webhooks.
// The fake (channel/fake), WhatsApp Cloud (channel/whatsapp) and Twilio
// SMS (channel/twilio) adapters pass one shared contract suite
// (channel/channeltest).
//
// Adapters hold the platform account's credentials (ADR-0038) and are
// safe for concurrent use. They never log a recipient's number or a
// message's text.
package channel

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
)

// Errors. Match with errors.Is. Anything else is transient: the send job
// retries it.
var (
	// ErrUndeliverable: the channel cannot reach this recipient (not on
	// WhatsApp, a number that does not take SMS). Try another channel.
	ErrUndeliverable = errors.New("channel: recipient unreachable on this channel")
	// ErrRejected: the channel refused the message itself (a template it
	// does not have, a value it does not take). Retrying will not help.
	ErrRejected = errors.New("channel: message rejected")
	// ErrAuth: the channel refused the platform's credentials.
	ErrAuth = errors.New("channel: credentials refused")
	// ErrForged: a webhook whose signature does not verify.
	ErrForged = errors.New("channel: webhook signature does not verify")
	// ErrBadWebhook: a webhook body that is not the channel's.
	ErrBadWebhook = errors.New("channel: unreadable webhook")
)

// Button is a quick reply: its label is the template's, its payload ours.
type Button struct {
	Payload string
}

// Message is one message to send. A template message (WhatsApp) has
// Template set; a text message (SMS) has Text.
type Message struct {
	To phone.E164
	// Template is the approved template's name, with Language its
	// locale ("ar", "en"), Params its body values {{1}}… in order and
	// Buttons its quick replies' payloads in order.
	Template string
	Language string
	Params   []string
	Buttons  []Button
	// Text is a text message's body.
	Text string
	// Ref is our reference (domain.Ref), which the channel returns with
	// every status of the message.
	Ref string
	// StatusURL receives the message's statuses, for channels that take
	// one per message (Twilio).
	StatusURL string
}

// Sent is a message the channel accepted.
type Sent struct {
	ProviderID string // WhatsApp's wamid, Twilio's SID
}

// EventKind is what a webhook event reports.
type EventKind string

// Event kinds.
const (
	// StatusEvent: a sent message's status changed.
	StatusEvent EventKind = "status"
	// ButtonEvent: the customer tapped a quick reply.
	ButtonEvent EventKind = "button"
	// TextEvent: the customer wrote to the platform's number.
	TextEvent EventKind = "text"
)

// Status is a message's state at the channel.
type Status string

// Statuses.
const (
	StatusSent      Status = "sent"
	StatusDelivered Status = "delivered"
	StatusRead      Status = "read"
	StatusFailed    Status = "failed"
)

// Event is one thing a webhook reports, normalised across channels. It
// carries no message text other than a button's payload, and the
// sender's number only for text events (they are placed by the text).
type Event struct {
	Kind EventKind `json:"kind"`
	// ID is the channel's id of this event, stable across its retries:
	// the provider id and status for a status, the inbound message's id
	// otherwise.
	ID string `json:"id"`
	// ProviderID is the message's id at the channel: for a status, our
	// sent message; for a button, the message whose button was tapped.
	ProviderID string `json:"provider_id"`
	// Ref is our reference, when the channel returned it (statuses).
	Ref       string    `json:"ref,omitempty"`
	Status    Status    `json:"status,omitempty"`
	ErrorCode string    `json:"error_code,omitempty"`
	Payload   string    `json:"payload,omitempty"` // a button's
	From      string    `json:"from,omitempty"`    // a text's sender, E.164
	Text      string    `json:"text,omitempty"`    // a text's body (placed by its code, 6c)
	At        time.Time `json:"at"`
}

// Webhook is a delivery as received.
type Webhook struct {
	Header map[string][]string
	Query  url.Values
	Body   []byte
}

// Channel is one channel's API.
type Channel interface {
	// Name is the channel's ID in the log and in webhook URLs.
	Name() string
	// Send sends m; it returns ErrUndeliverable, ErrRejected, ErrAuth or
	// a transient error. It sends at most once per call: there is no
	// idempotency key, so a lost answer is a transient error and the
	// caller decides whether to send again.
	Send(ctx context.Context, m Message) (Sent, error)
	// VerifyWebhook checks a delivery's signature and reads its events:
	// ErrForged or ErrBadWebhook.
	VerifyWebhook(w Webhook) ([]Event, error)
	// Undeliverable reports whether a failed status's error code means
	// the recipient cannot be reached on this channel (try another).
	Undeliverable(code string) bool
}

// Fetcher is a channel that answers what became of a message (Twilio):
// its webhooks are then only hints, as ADR-0024 asks. Status is "" while
// the message is still on its way.
type Fetcher interface {
	Fetch(ctx context.Context, providerID string) (status Status, errorCode string, err error)
}
