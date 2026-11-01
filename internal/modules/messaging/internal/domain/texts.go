package domain

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Locales a message is written in.
const (
	Arabic  = "ar"
	English = "en"
)

// Locales lists the message locales.
func Locales() []string { return []string{Arabic, English} }

// ValidLocale reports whether l is a message locale.
func ValidLocale(l string) bool { return l == Arabic || l == English }

// Template is a WhatsApp template the platform's account had approved
// by Meta, in both locales (docs/modules/messaging.md has their texts).
type Template struct {
	Name string
	// Buttons are the quick replies, in the template's order.
	Buttons []Reply
}

// WhatsAppTemplate is kind's template on the platform's number.
func WhatsAppTemplate(k Kind) (Template, bool) {
	switch k {
	case OrderConfirmation:
		return Template{Name: "order_confirmation_v1", Buttons: []Reply{Confirm, Cancel}}, true
	case DeliveryPrepay:
		return Template{Name: "order_delivery_prepay_v1"}, true
	}
	return Template{}, false
}

// maxParam bounds one template value; WhatsApp refuses values with line
// breaks, tabs or more than four spaces in a row.
const maxParam = 60

// TemplateParams are the template body's values, {{1}}…{{4}}: the
// customer's name, the store's name, the order's reference and total;
// a prepaid delivery's {{4}} is the amount due and {{5}} the link.
func TemplateParams(k Kind, v Vars) []string {
	switch k {
	case OrderConfirmation:
		return []string{param(v.CustomerName), param(v.StoreName), param(v.OrderRef), param(v.Total)}
	case DeliveryPrepay:
		return []string{param(v.CustomerName), param(v.StoreName), param(v.OrderRef), param(v.Due), link(v.ConfirmURL)}
	}
	return nil
}

// maxLink bounds a link in a template: our links are a store's host and
// a 22-character token.
const maxLink = 300

// link makes a URL a template value: whole (a cut link opens nothing),
// without spaces; "-" when it cannot be one.
func link(s string) string {
	if s == "" || len(s) > maxLink || strings.IndexFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return "-"
	}
	return s
}

// param makes a value WhatsApp takes: one line, single spaces, at most
// maxParam characters, never empty.
func param(s string) string {
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
	if utf8.RuneCountInString(s) > maxParam {
		s = string([]rune(s)[:maxParam])
	}
	if s == "" {
		return "-"
	}
	return s
}

// MaxSMSBody bounds a store's SMS text: six Arabic (UCS-2) segments.
const MaxSMSBody = 400

// DefaultSMS is kind's SMS text in locale until the store writes its own.
func DefaultSMS(k Kind, locale string) string {
	switch {
	case k == OrderConfirmation && locale == English:
		return "{{store}}: Hi {{name}}, please confirm your order {{ref}} ({{total}}): {{url}}"
	case k == OrderConfirmation:
		return "{{store}}: أهلاً {{name}}، أكّد طلبك رقم {{ref}} ({{total}}) من هنا: {{url}}"
	case k == DeliveryPrepay && locale == English:
		return "{{store}}: Hi {{name}}, to complete your order {{ref}}, pay the delivery ({{due}}) upfront within 6 hours: {{url}}. You pay the rest on delivery."
	case k == DeliveryPrepay:
		return "{{store}}: أهلاً {{name}}، لإتمام طلبك رقم {{ref}} ادفع مصاريف التوصيل ({{due}}) مقدمًا خلال 6 ساعات: {{url}} وتدفع الباقي عند الاستلام."
	}
	return ""
}

// ValidSMS reports whether body can be a store's SMS text: 1 to
// MaxSMSBody characters, only known placeholders, and the link to answer
// for kinds that need one.
func ValidSMS(k Kind, body string) bool {
	n := utf8.RuneCountInString(body)
	if n == 0 || n > MaxSMSBody || !utf8.ValidString(body) {
		return false
	}
	// The placeholders an SMS text may use.
	rest := strings.NewReplacer("{{name}}", "", "{{store}}", "", "{{ref}}", "", "{{total}}", "", "{{url}}", "").Replace(body)
	if strings.Contains(rest, "{{") || strings.Contains(rest, "}}") {
		return false
	}
	return k != OrderConfirmation || strings.Contains(body, "{{url}}")
}

// RenderSMS fills body's placeholders from v.
func RenderSMS(body string, v Vars) string {
	return strings.NewReplacer(
		"{{name}}", param(v.CustomerName), "{{store}}", param(v.StoreName), "{{ref}}", param(v.OrderRef),
		"{{total}}", param(v.Total), "{{url}}", v.ConfirmURL, "{{due}}", param(v.Due),
	).Replace(body)
}

// Text is a store's SMS text for a kind and locale: its own (Custom),
// or the default.
type Text struct {
	Kind      Kind
	Locale    string
	SMSBody   string
	Custom    bool
	UpdatedAt *time.Time
}

// The ack's outcomes beyond a confirmation's status (ADR-0070):
// OutcomeAwaitingCall, a yes that left the order pending for the store's
// call; OutcomeDepositDue, a yes to an order whose delivery is unpaid.
const (
	OutcomeAwaitingCall = "awaiting_call"
	OutcomeDepositDue   = "deposit_due"
)

// AckText is the text thanking a customer who answered on WhatsApp, by
// what the answer did (Vars.Outcome).
func AckText(locale string, v Vars) string {
	name, ref, store := param(v.CustomerName), param(v.OrderRef), param(v.StoreName)
	if locale == English {
		switch v.Outcome {
		case OutcomeAwaitingCall:
			return "Thank you " + name + ", we have your answer. " + store + " will call you to confirm your order " + ref + "."
		case OutcomeDepositDue:
			return "Thank you " + name + ". Your order " + ref + " from " + store + " is confirmed once its delivery is paid, from the link we sent you."
		case string(Confirmed):
			return "Thank you " + name + ", your order " + ref + " from " + store + " is confirmed. We will let you know when it ships."
		case string(Cancelled):
			return "Your order " + ref + " from " + store + " is cancelled."
		}
		return "Your order " + ref + " from " + store + " no longer needs your answer. Contact the store with any question."
	}
	switch v.Outcome {
	case OutcomeAwaitingCall:
		return "شكراً " + name + "، وصلنا ردك، وسيتصل بك " + store + " لتأكيد طلبك رقم " + ref + "."
	case OutcomeDepositDue:
		return "شكراً " + name + "، يتأكد طلبك رقم " + ref + " من " + store + " بعد دفع مصاريف التوصيل من الرابط الذي أرسلناه لك."
	case string(Confirmed):
		return "شكراً " + name + "، تم تأكيد طلبك رقم " + ref + " من " + store + ". سنبلغك عند شحنه."
	case string(Cancelled):
		return "تم إلغاء طلبك رقم " + ref + " من " + store + "."
	}
	return "طلبك رقم " + ref + " من " + store + " لم يعد ينتظر ردك. للاستفسار تواصل مع المتجر."
}
