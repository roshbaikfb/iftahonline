package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
	"strings"
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
	// WACode is the code of the customer's ready-made WhatsApp text
	// ("" when the platform has no WhatsApp number to confirm on).
	WACode string
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
	Period   string `json:"period,omitempty"`  // YYYY-MM
	Feature  string `json:"feature,omitempty"` // a plan limit's; "" is the messages quota
	// NeedsCall: an order_confirmed whose order stays pending, because
	// the store's risk rules want the customer's yes verified by phone
	// (ADR-0066).
	NeedsCall bool `json:"needs_call,omitempty"`
}

// QuotaNotice is the warning for a threshold reached in a month, once
// per month and threshold.
func QuotaNotice(kind NoticeKind, period time.Time, used, limit int64) Notification {
	p := period.Format("2006-01")
	return Notification{Kind: kind, DedupKey: p, Data: NoticeData{Used: used, Limit: limit, Period: p}}
}

// PlanLimitNotice is the warning for a plan's monthly limit of feature
// near (QuotaNearly) or reached (QuotaReached), once per feature, month
// and threshold. It is a quota notice with a feature, so the API's kinds
// stay as they were (ADR-0046).
func PlanLimitNotice(kind NoticeKind, feature string, period time.Time, used, limit int64) Notification {
	p := period.Format("2006-01")
	return Notification{Kind: kind, DedupKey: feature + ":" + p, Data: NoticeData{Used: used, Limit: limit, Period: p, Feature: feature}}
}

// OrderNotice is an order's notice, once per order and kind.
func OrderNotice(kind NoticeKind, order ulid.ID, ref string) Notification {
	return Notification{Kind: kind, DedupKey: order.String(), Data: NoticeData{OrderID: order.String(), OrderRef: ref}}
}

// Email is a notice's email in locale: its subject and text.
func (n Notification) Email(locale, storeName, dashboardURL string) (subject, text string) {
	used, limit := strconv.FormatInt(n.Data.Used, 10), strconv.FormatInt(n.Data.Limit, 10)
	if n.Data.Feature != "" {
		return n.planLimitEmail(locale, storeName, dashboardURL+"/billing", used, limit)
	}
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

// planLimitEmail is a plan-limit warning's email. Orders are never
// refused, so it asks for an upgrade, not for action.
func (n Notification) planLimitEmail(locale, storeName, link, used, limit string) (subject, text string) {
	what, whatAr := "orders", "طلب"
	if n.Data.Feature != "orders_per_month" {
		what, whatAr = n.Data.Feature, n.Data.Feature
	}
	if locale == English {
		if n.Kind == QuotaReached {
			return storeName + ": your plan's monthly " + what + " are used up",
				"Your store has reached its plan's " + limit + " " + what + " for " + n.Data.Period + ".\n" +
					"New orders still come in; upgrade your plan to stay within it.\n\n" + link
		}
		return storeName + ": 80% of your plan's monthly " + what,
			"Your store has used " + used + " of its plan's " + limit + " " + what + " for " + n.Data.Period + ".\n\n" + link
	}
	if n.Kind == QuotaReached {
		return storeName + ": وصلت إلى حد باقتك هذا الشهر",
			"وصل متجرك إلى حد باقته (" + limit + " " + whatAr + ") في " + n.Data.Period + ".\n" +
				"الطلبات الجديدة ما زالت تصلك؛ رقِّ باقتك لتبقى ضمن حدها.\n\n" + link
	}
	return storeName + ": استُخدم 80% من حد باقتك هذا الشهر",
		"استخدم متجرك " + used + " من " + limit + " " + whatAr + " يسمح بها حد باقته في " + n.Data.Period + ".\n\n" + link
}

// crockford is the alphabet of WhatsApp codes (no I, L, O, U).
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// CodeLen is a WhatsApp code's length: 40 bits, unique across stores.
const CodeLen = 8

// NewCode draws a WhatsApp code: the short reference a customer's
// ready-made WhatsApp text carries, so the platform's one number knows
// whose order it confirms.
func NewCode() (string, error) {
	b := make([]byte, CodeLen)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("messaging: code: %w", err)
	}
	for i := range b {
		b[i] = crockford[b[i]&31]
	}
	return string(b), nil
}

// FindCode finds a WhatsApp code in a customer's text: the first word of
// exactly CodeLen characters of the alphabet, in any case, with Arabic-
// Indic digits and the usual confusions read as the digits they look
// like (O as 0, I and L as 1). Anything else separates words.
func FindCode(text string) (string, bool) {
	word := make([]byte, 0, CodeLen+1)
	for _, r := range text + " " {
		if c, ok := codeChar(r); ok {
			if len(word) <= CodeLen {
				word = append(word, c)
			}
			continue
		}
		if len(word) == CodeLen {
			return string(word), true
		}
		word = word[:0]
	}
	return "", false
}

// codeChar reads one character of a code.
func codeChar(r rune) (byte, bool) {
	switch {
	case r >= 'a' && r <= 'z':
		r -= 'a' - 'A'
	case r >= '٠' && r <= '٩': // Arabic-Indic digits, as some keyboards type them
		r = '0' + (r - '٠')
	}
	switch r {
	case 'O':
		return '0', true
	case 'I', 'L':
		return '1', true
	}
	if i := strings.IndexRune(crockford, r); i >= 0 {
		return crockford[i], true
	}
	return 0, false
}

// CustomerConfirmText is the ready-made text a customer sends from the
// thank-you page's link, in the page's locale: it carries the code.
func CustomerConfirmText(locale, code string) string {
	if locale == English {
		return "I confirm my order " + code
	}
	return "أؤكد طلبي رقم " + code
}

// WhatsAppLink is the wa.me link that opens a chat with number (E.164)
// with text ready to send.
func WhatsAppLink(number, text string) string {
	return "https://wa.me/" + strings.TrimPrefix(number, "+") + "?text=" + url.QueryEscape(text)
}
