package domain

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Locales a message is written in.
const (
	Arabic  = "ar"
	English = "en"
)

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
	}
	return Template{}, false
}

// maxParam bounds one template value; WhatsApp refuses values with line
// breaks, tabs or more than four spaces in a row.
const maxParam = 60

// TemplateParams are the template body's values, {{1}}…{{4}}: the
// customer's name, the store's name, the order's reference and total.
func TemplateParams(k Kind, v Vars) []string {
	switch k {
	case OrderConfirmation:
		return []string{param(v.CustomerName), param(v.StoreName), param(v.OrderRef), param(v.Total)}
	}
	return nil
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
		"{{total}}", param(v.Total), "{{url}}", v.ConfirmURL,
	).Replace(body)
}
