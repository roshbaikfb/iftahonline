package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Settings are a store's messaging choices.
type Settings struct {
	// ConfirmCOD asks every cash-on-delivery customer to confirm.
	ConfirmCOD bool
	// AutoCancelHours: an order whose customer got the message but did
	// not answer is cancelled this long after it was placed.
	AutoCancelHours int
}

// Bounds of AutoCancelHours.
const (
	MinAutoCancelHours = 1
	MaxAutoCancelHours = 72
)

// DefaultSettings are a store's until it chooses.
func DefaultSettings() Settings { return Settings{ConfirmCOD: true, AutoCancelHours: 24} }

// Valid reports whether s can be saved.
func (s Settings) Valid() bool {
	return s.AutoCancelHours >= MinAutoCancelHours && s.AutoCancelHours <= MaxAutoCancelHours
}

// ConfirmationStatus is where an order's confirmation is.
type ConfirmationStatus string

// Confirmation statuses.
const (
	// Pending: the message is on its way or waits for an answer.
	Pending ConfirmationStatus = "pending"
	// Confirmed and Cancelled: the customer answered, and the order moved.
	Confirmed ConfirmationStatus = "confirmed"
	Cancelled ConfirmationStatus = "cancelled"
	// Expired: no answer in time; the order was cancelled.
	Expired ConfirmationStatus = "expired"
	// QuotaSkipped: the store's quota was used up; nothing was sent.
	QuotaSkipped ConfirmationStatus = "skipped"
	// Unreached: the message never reached the customer; the order is
	// left to the merchant.
	Unreached ConfirmationStatus = "unreached"
	// Stale: the order moved before the answer or the deadline (the
	// merchant handled it); nothing changed.
	Stale ConfirmationStatus = "stale"
)

// Who answered a confirmation, as the order's timeline records it
// (orders' actor format: "<kind>:<name>", name in [a-z0-9_]).
const (
	ActorWhatsApp   = "customer:whatsapp"
	ActorSMSLink    = "customer:sms"
	ActorAutoCancel = "system:auto_cancel"
)

// Confirmation is an order's request to its customer.
type Confirmation struct {
	OrderID    ulid.ID
	MessageID  *ulid.ID
	Status     ConfirmationStatus
	ExpiresAt  time.Time
	AnsweredAt *time.Time
	AnsweredBy string
	CreatedAt  time.Time
}

// tokenBytes is the SMS link token's entropy: 128 bits.
const tokenBytes = 16

// NewToken makes an SMS link token and the hash that is stored.
func NewToken() (token string, hash []byte, err error) {
	b := make([]byte, tokenBytes)
	if _, err = rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("messaging: token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token), nil
}

// HashToken is what is stored of a token.
func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// ConfirmPath is the storefront path of an SMS link.
func ConfirmPath(locale, token string) string { return "/" + locale + "/o/" + token }

// NoticeKind is what a merchant notification is about.
type NoticeKind string

// Notification kinds.
const (
	QuotaNearly           NoticeKind = "quota_nearly"
	QuotaReached          NoticeKind = "quota_reached"
	OrderConfirmedNotice  NoticeKind = "order_confirmed"
	OrderCancelledNotice  NoticeKind = "order_cancelled"
	OrderAutoCancelled    NoticeKind = "order_auto_cancelled"
	ConfirmationUnreached NoticeKind = "confirmation_unreached"
)

// Emailed reports whether the merchant also gets k by email: the quota
// warnings only (an order's notices would flood the inbox).
func (k NoticeKind) Emailed() bool { return k == QuotaNearly || k == QuotaReached }

// Notification is a notice to the store's staff.
type Notification struct {
	ID        ulid.ID
	Kind      NoticeKind
	DedupKey  string
	Data      NoticeData
	ReadAt    *time.Time
	CreatedAt time.Time
}

// NoticeData are a notice's facts: an order's, or the quota's.
type NoticeData struct {
	OrderID  string `json:"order_id,omitempty"`
	OrderRef string `json:"order_ref,omitempty"`
	Used     int64  `json:"used,omitempty"`
	Limit    int64  `json:"limit,omitempty"`
	Period   string `json:"period,omitempty"` // YYYY-MM
}

// QuotaNotice is the warning for a threshold reached in a month, once
// per month and threshold.
func QuotaNotice(kind NoticeKind, period time.Time, used, limit int64) Notification {
	p := period.Format("2006-01")
	return Notification{Kind: kind, DedupKey: p, Data: NoticeData{Used: used, Limit: limit, Period: p}}
}

// OrderNotice is an order's notice, once per order and kind.
func OrderNotice(kind NoticeKind, order ulid.ID, ref string) Notification {
	return Notification{Kind: kind, DedupKey: order.String(), Data: NoticeData{OrderID: order.String(), OrderRef: ref}}
}

// Email is a notice's email in locale: its subject and text.
func (n Notification) Email(locale, storeName, dashboardURL string) (subject, text string) {
	used, limit := strconv.FormatInt(n.Data.Used, 10), strconv.FormatInt(n.Data.Limit, 10)
	link := dashboardURL + "/messages"
	if locale == English {
		switch n.Kind {
		case QuotaReached:
			return storeName + ": monthly messages used up",
				"Your store has used all " + limit + " customer messages for " + n.Data.Period + ".\n" +
					"Order confirmations are not being sent until next month; please confirm new cash-on-delivery orders yourself.\n\n" + link
		default:
			return storeName + ": 80% of monthly messages used",
				"Your store has used " + used + " of its " + limit + " customer messages for " + n.Data.Period + ".\n\n" + link
		}
	}
	switch n.Kind {
	case QuotaReached:
		return storeName + ": نفدت رسائل هذا الشهر",
			"استخدم متجرك كل رسائل العملاء المتاحة هذا الشهر (" + limit + " رسالة) في " + n.Data.Period + ".\n" +
				"لن تُرسل رسائل تأكيد الطلبات حتى الشهر القادم؛ أكّد طلبات الدفع عند الاستلام الجديدة بنفسك.\n\n" + link
	default:
		return storeName + ": استُخدم 80% من رسائل هذا الشهر",
			"استخدم متجرك " + used + " من " + limit + " رسالة للعملاء في " + n.Data.Period + ".\n\n" + link
	}
}
