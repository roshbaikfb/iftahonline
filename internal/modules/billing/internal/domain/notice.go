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
	NoticeSuspended     NoticeKind = "suspended"      // the grace ran out: the store is on the free plan until ClosesAt
	NoticeHeld          NoticeKind = "held"           // still unpaid: the store is closed, its data kept
	NoticePaid          NoticeKind = "paid"           // an invoice was paid (and the store is back if it was not)
	// The prepaid balance (ADR-0084); TotalMinor is the balance, or what
	// it owes.
	NoticeBalanceLow      NoticeKind = "balance_low"      // under half the minimum
	NoticeBalanceEmpty    NoticeKind = "balance_empty"    // nothing left or owed: past due until GraceUntil
	NoticeBalanceRestored NoticeKind = "balance_restored" // back above nothing: the store is back
	NoticeToppedUp        NoticeKind = "topped_up"        // a top-up was paid (ADR-0085)
)

// Notice is a billing notice's facts.
type Notice struct {
	Kind       NoticeKind `json:"kind"`
	Invoice    string     `json:"invoice,omitempty"`
	TotalMinor int64      `json:"total_minor,omitempty"`
	Currency   string     `json:"currency,omitempty"`
	RetryAt    time.Time  `json:"retry_at,omitzero"`
	GraceUntil time.Time  `json:"grace_until,omitzero"`
	ClosesAt   time.Time  `json:"closes_at,omitzero"` // when the storefront closes if still unpaid
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
	failedSubject, failed, retry, free, closes, payLink string
	suspendedSubject, suspended                         string
	heldSubject, held                                   string
	paidSubject, paid                                   string
	lowSubject, low, emptySubject, empty, topUp         string
	restoredSubject, restored                           string
	toppedSubject, topped                               string
}

func textsFor(locale string) noticeTexts {
	if locale == "en" {
		return noticeTexts{
			failedSubject: ": payment failed", failed: "We could not charge invoice %s (%s).",
			retry: " We will try again on %s.", free: " If it stays unpaid, the store moves to the free plan on %s.",
			closes:           " The store closes on %s; its data is kept.",
			payLink:          "\n\nPay or change the card: ",
			suspendedSubject: ": the store is on the free plan",
			suspended: "The invoice is unpaid, so the store is on the free plan: it stays open with the platform's ads, " +
				"without card payments, coupons or pixels. Pay the invoice to get your plan back.",
			heldSubject: ": store closed for an unpaid invoice",
			held:        "The store is closed: shoppers see a maintenance page. Its data is kept; pay the invoice to open it again.\n\n",
			paidSubject: ": payment received", paid: "Invoice %s (%s) is paid. Thank you.\n\n",
			lowSubject: ": your balance is low", low: "Your balance is %s. Top it up before it runs out so nothing stops in your store.",
			emptySubject: ": your balance ran out", empty: "Your balance owes %s in fees of delivered orders. Top it up; what is owed is taken from it first.",
			topUp:           "\n\nTop up: ",
			restoredSubject: ": your store is back", restored: "Your balance is %s again, and your store is back on its plan. Thank you.\n\n",
			toppedSubject: ": balance topped up", topped: "We added %s to your balance. Thank you.\n\n",
		}
	}
	return noticeTexts{
		failedSubject: ": تعذّر الدفع", failed: "تعذّر تحصيل الفاتورة %s (%s).",
		retry: " سنحاول مرة أخرى يوم %s.", free: " إذا لم تُدفع، ينتقل المتجر إلى الباقة المجانية يوم %s.",
		closes:           " يُغلق المتجر يوم %s مع الاحتفاظ ببياناته.",
		payLink:          "\n\nادفع أو غيّر البطاقة: ",
		suspendedSubject: ": متجرك على الباقة المجانية",
		suspended: "الفاتورة لم تُدفع، فصار متجرك على الباقة المجانية: يبقى مفتوحًا مع إعلانات المنصة، " +
			"ودون الدفع بالبطاقة والكوبونات والبكسلات. ادفع الفاتورة لترجع باقتك.",
		heldSubject: ": أُغلق المتجر لفاتورة غير مدفوعة",
		held:        "أُغلق المتجر: يرى العملاء صفحة صيانة. بياناته محفوظة؛ ادفع الفاتورة ليعود.\n\n",
		paidSubject: ": تم استلام الدفع", paid: "دُفعت الفاتورة %s (%s). شكرًا لك.\n\n",
		lowSubject: ": رصيدك منخفض", low: "رصيدك %s. اشحنه قبل أن ينفد حتى لا يتوقف شيء في متجرك.",
		emptySubject: ": نفد رصيدك", empty: "عليك %s رسوم طلبات سُلّمت. اشحن رصيدك؛ يُخصم المستحق من أول شحنة.",
		topUp:           "\n\nاشحن رصيدك: ",
		restoredSubject: ": رجع متجرك", restored: "رصيدك الآن %s، ورجع متجرك على باقته. شكرًا لك.\n\n",
		toppedSubject: ": تم شحن رصيدك", topped: "أضفنا %s إلى رصيدك. شكرًا لك.\n\n",
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
			text += fmt.Sprintf(w.free, day(n.GraceUntil))
		}
		if !n.ClosesAt.IsZero() {
			text += fmt.Sprintf(w.closes, day(n.ClosesAt))
		}
		return store + w.failedSubject, text + w.payLink + link
	case NoticeSuspended:
		text = w.suspended
		if !n.ClosesAt.IsZero() {
			text += fmt.Sprintf(w.closes, day(n.ClosesAt))
		}
		return store + w.suspendedSubject, text + "\n\n" + link
	case NoticeHeld:
		return store + w.heldSubject, w.held + link
	case NoticeBalanceLow:
		return store + w.lowSubject, fmt.Sprintf(w.low, sum) + w.topUp + link
	case NoticeBalanceEmpty:
		text = fmt.Sprintf(w.empty, sum)
		if !n.GraceUntil.IsZero() {
			text += fmt.Sprintf(w.free, day(n.GraceUntil))
		}
		if !n.ClosesAt.IsZero() {
			text += fmt.Sprintf(w.closes, day(n.ClosesAt))
		}
		return store + w.emptySubject, text + w.topUp + link
	case NoticeBalanceRestored:
		return store + w.restoredSubject, fmt.Sprintf(w.restored, sum) + link
	case NoticeToppedUp:
		return store + w.toppedSubject, fmt.Sprintf(w.topped, sum) + link
	default:
		return store + w.paidSubject, fmt.Sprintf(w.paid, n.Invoice, sum) + link
	}
}
