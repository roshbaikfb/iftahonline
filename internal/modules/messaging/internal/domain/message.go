// Package domain holds the rules of customer messaging (ADR-0039): what
// a message is, how its status moves, the references we hand the
// channels so their webhooks find the message again, and the default
// texts.
package domain

import (
	"errors"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Kind is what a message is for.
type Kind string

// Kinds.
const (
	// OrderConfirmation asks a cash-on-delivery customer to confirm or
	// cancel their order (P6 6c), with Confirm and Cancel buttons on
	// WhatsApp.
	OrderConfirmation Kind = "order_confirmation"
	// AnswerAck thanks a customer who answered on WhatsApp, and says
	// what became of the order: a text inside the conversation they
	// just opened, free on WhatsApp, so it costs no quota and never goes
	// by SMS.
	AnswerAck Kind = "order_answer_ack"
	// DeliveryPrepay sends a cash order's customer the link to pay its
	// delivery upfront (ADR-0070): to the phone, never to whoever filled
	// in the order form, so the page after checkout says nothing of it.
	// Its texts are the platform's.
	DeliveryPrepay Kind = "order_delivery_prepay"
)

// Valid reports whether k is a kind.
func (k Kind) Valid() bool { return k == OrderConfirmation || k == AnswerAck || k == DeliveryPrepay }

// Metered reports whether a message of kind k costs a quota unit.
func (k Kind) Metered() bool { return k != AnswerAck }

// WhatsAppOnly reports whether k goes by WhatsApp only (never SMS).
func (k Kind) WhatsAppOnly() bool { return k == AnswerAck }

// Kinds lists the kinds whose SMS text a store writes.
func Kinds() []Kind { return []Kind{OrderConfirmation} }

// Channel is how a message went out.
type Channel string

// Channels.
const (
	WhatsApp Channel = "whatsapp"
	SMS      Channel = "sms"
)

// Status is where a message is.
type Status string

// Statuses. A message moves forward only: queued, sent, delivered, read;
// failed from queued or sent; skipped never moves.
const (
	// Queued: counted against the quota, its send job enqueued.
	Queued Status = "queued"
	// Sent: the channel accepted it.
	Sent Status = "sent"
	// Delivered: it reached the customer's phone.
	Delivered Status = "delivered"
	// Read: the customer opened it (WhatsApp only).
	Read Status = "read"
	// Failed: no channel could deliver it; its quota unit was returned.
	Failed Status = "failed"
	// Skipped: not sent because the store's monthly quota is used up.
	Skipped Status = "skipped_quota"
)

// rank orders the statuses a message moves through.
func (s Status) rank() int {
	switch s {
	case Queued:
		return 0
	case Sent:
		return 1
	case Delivered:
		return 2
	case Read:
		return 3
	}
	return -1
}

// Valid reports whether s is a status.
func (s Status) Valid() bool { return s.rank() >= 0 || s == Failed || s == Skipped }

// Advances reports whether a message at s may move to next. Statuses
// arrive out of order and more than once: a later one never goes back
// (a "delivered" after "read" is ignored), and a message a channel
// delivered does not fail.
func (s Status) Advances(next Status) bool {
	switch {
	case s == Failed || s == Skipped:
		return false
	case next == Failed:
		return s == Queued || s == Sent
	default:
		return next.rank() > s.rank()
	}
}

// Reply is the customer's answer by button.
type Reply string

// Replies.
const (
	NoReply Reply = ""
	Confirm Reply = "confirm"
	Cancel  Reply = "cancel"
)

// Message is one customer message as the log keeps it. The recipient and
// the texts' values are sealed in the content (only the job that sends
// opens them); the log shows the masked number.
type Message struct {
	ID      ulid.ID
	Kind    Kind
	Locale  string
	OrderID *ulid.ID
	Status  Status
	Channel Channel // "" until sent
	// Provider is the adapter that sent it ("whatsapp", "twilio",
	// "fake_sms"): whose statuses it takes.
	Provider string
	ToMasked string
	// ProviderID is the channel's id of the message (WhatsApp's wamid,
	// Twilio's SID).
	ProviderID string
	// Fallback is set once the message went to SMS after WhatsApp could
	// not deliver it.
	Fallback bool
	// ErrorCode is the channel's code for the last failure.
	ErrorCode string
	// QuotaPeriod is the month its quota unit was counted in (nil: none
	// was counted).
	QuotaPeriod *time.Time
	// Content is sealed; only the send job opens it.
	Content   []byte
	Reply     Reply
	RepliedAt *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Errors.
var (
	ErrNotFound = errors.New("messaging: message not found")
	ErrInvalid  = errors.New("messaging: invalid message")
)

// Content is what a message says, sealed at rest: the recipient and the
// values its texts fill in.
type Content struct {
	To   string `json:"to"` // E.164
	Vars Vars   `json:"vars"`
}

// Vars are the values a message's texts fill in.
type Vars struct {
	CustomerName string `json:"customer_name"`
	StoreName    string `json:"store_name"`
	OrderRef     string `json:"order_ref"`
	Total        string `json:"total"` // formatted, e.g. "520.00 EGP"
	// ConfirmURL is where an SMS customer confirms or cancels (6c), or
	// pays a prepaid delivery (DeliveryPrepay).
	ConfirmURL string `json:"confirm_url,omitempty"`
	// Due is a prepaid delivery's amount, formatted (DeliveryPrepay).
	Due string `json:"due,omitempty"`
	// Outcome is what an answer did (AnswerAck): confirmed, cancelled or
	// stale.
	Outcome string `json:"outcome,omitempty"`
}

// CreatedAtOf is the creation time a message ID encodes; the log's
// partition key.
func CreatedAtOf(id ulid.ID) time.Time { return id.Time().UTC() }
