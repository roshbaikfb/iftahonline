package domain

import (
	"fmt"
	"time"
)

// NoticeKind is what a billing notice tells a store's owners.
type NoticeKind string

// Notice kinds.
const (
	NoticePaymentFailed NoticeKind = "payment_failed" // a charge failed: when it is retried, and the grace
	NoticeSuspended     NoticeKind = "suspended"      // the grace ran out: the store is closed, its data kept
	NoticePaid          NoticeKind = "paid"           // an invoice was paid (and the store is back if it was not)
)

// Notice is a billing notice's facts.
type Notice struct {
	Kind       NoticeKind `json:"kind"`
	Invoice    string     `json:"invoice,omitempty"`
	TotalMinor int64      `json:"total_minor,omitempty"`
	Currency   string     `json:"currency,omitempty"`
	RetryAt    time.Time  `json:"retry_at,omitzero"`
	GraceUntil time.Time  `json:"grace_until,omitzero"`
	Reason     string     `json:"reason,omitempty"`
}

func amount(minor int64, cur string) string {
	return fmt.Sprintf("%d.%02d %s", minor/100, minor%100, cur)
}

// Amount is the notice's total as written in it, e.g. "682.86 EGP".
func (n Notice) Amount() string { return amount(n.TotalMinor, n.Currency) }

// Date is a notice's day as written in it (UTC), "" for none.
func Date(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

// noticeTexts are a language's words for the notices; %s and %d are
// filled in Email.
type noticeTexts struct {
	failedSubject, failed, retry, closes, payLink string
	suspendedSubject, suspended                   string
	paidSubject, paid                             string
}

func textsFor(locale string) noticeTexts {
	if locale == "en" {
		return noticeTexts{
			failedSubject: ": payment failed", failed: "We could not charge invoice %s (%s).",
			retry: " We will try again on %s.", closes: " If it stays unpaid, the store closes on %s; its data is kept.",
			payLink:          "\n\nPay or change the card: ",
			suspendedSubject: ": store closed for an unpaid invoice",
			suspended:        "The store is closed: shoppers see a maintenance page. Its data is kept; pay the invoice to open it again.\n\n",
			paidSubject:      ": payment received", paid: "Invoice %s (%s) is paid. Thank you.\n\n",
		}
	}
	return noticeTexts{
		failedSubject: ": تعذّر الدفع", failed: "تعذّر تحصيل الفاتورة %s (%s).",
		retry: " سنحاول مرة أخرى يوم %s.", closes: " إذا لم تُدفع، يُغلق المتجر يوم %s مع الاحتفاظ ببياناته.",
		payLink:          "\n\nادفع أو غيّر البطاقة: ",
		suspendedSubject: ": أُغلق المتجر لفاتورة غير مدفوعة",
		suspended:        "أُغلق المتجر: يرى العملاء صفحة صيانة. بياناته محفوظة؛ ادفع الفاتورة ليعود.\n\n",
		paidSubject:      ": تم استلام الدفع", paid: "دُفعت الفاتورة %s (%s). شكرًا لك.\n\n",
	}
}

// Email is the notice's email to the owners of store, in locale, with a
// link to the store's billing page.
func (n Notice) Email(locale, store, link string) (subject, text string) {
	w := textsFor(locale)
	day := func(t time.Time) string { return t.UTC().Format("2006-01-02") }
	sum := amount(n.TotalMinor, n.Currency)
	switch n.Kind {
	case NoticePaymentFailed:
		text = fmt.Sprintf(w.failed, n.Invoice, sum)
		if !n.RetryAt.IsZero() {
			text += fmt.Sprintf(w.retry, day(n.RetryAt))
		}
		if !n.GraceUntil.IsZero() {
			text += fmt.Sprintf(w.closes, day(n.GraceUntil))
		}
		return store + w.failedSubject, text + w.payLink + link
	case NoticeSuspended:
		return store + w.suspendedSubject, w.suspended + link
	default:
		return store + w.paidSubject, fmt.Sprintf(w.paid, n.Invoice, sum) + link
	}
}
