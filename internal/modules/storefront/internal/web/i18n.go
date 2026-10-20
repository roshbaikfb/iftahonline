package web

import (
	"strconv"
	"strings"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
)

// Locale is a shopper-facing language. Arabic is the platform's first
// language: pages are RTL-first, and English mirrors them.
type Locale string

// Locales.
const (
	Arabic  Locale = "ar"
	English Locale = "en"
)

// ParseLocale returns the locale for a store's setting ("ar", "ar-EG",
// "en", ...), Arabic when unknown.
func ParseLocale(s string) Locale {
	if strings.HasPrefix(strings.ToLower(s), "en") {
		return English
	}
	return Arabic
}

// Other is the store's second language.
func (l Locale) Other() Locale {
	if l == Arabic {
		return English
	}
	return Arabic
}

// Dir is the text direction.
func (l Locale) Dir() string {
	if l == Arabic {
		return "rtl"
	}
	return "ltr"
}

// Msg is a fixed interface string.
type Msg int

// Messages.
const (
	MsgHome Msg = iota
	MsgAddToCart
	MsgOrderNow
	MsgSoldOut
	MsgOptions
	MsgQuantity
	MsgDescription
	MsgNewArrivals
	MsgProducts
	MsgReviews
	MsgFAQ
	MsgOfferEnds
	MsgDays
	MsgHours
	MsgMinutes
	MsgSeconds
	MsgShowMore
	MsgNoProducts
	MsgNotFoundTitle
	MsgNotFoundText
	MsgBackHome
	MsgLanguageName
	MsgPoweredBy
	MsgRatingOf
	MsgSkipToContent
	MsgPolicyShipping
	MsgPolicyRefund
	MsgPolicyPrivacy
	MsgPolicyTerms
	MsgBadgeShipping
	MsgBadgeCOD
	MsgBadgeReturns
	MsgBadgeSecure
	MsgBadgeSupport
	MsgBadgeQuality
	msgCount
)

// messages holds each message in both languages side by side, so a
// translation cannot go missing in one of them.
var messages = [msgCount]struct{ ar, en string }{ //nolint:gochecknoglobals // constant translation table
	MsgHome:           {"الرئيسية", "Home"},
	MsgAddToCart:      {"أضف إلى السلة", "Add to cart"},
	MsgOrderNow:       {"اطلب الآن", "Order now"},
	MsgSoldOut:        {"نفدت الكمية", "Sold out"},
	MsgOptions:        {"الاختيار", "Option"},
	MsgQuantity:       {"الكمية", "Quantity"},
	MsgDescription:    {"الوصف", "Description"},
	MsgNewArrivals:    {"وصل حديثًا", "New arrivals"},
	MsgProducts:       {"المنتجات", "Products"},
	MsgReviews:        {"آراء العملاء", "Customer reviews"},
	MsgFAQ:            {"الأسئلة الشائعة", "Frequently asked questions"},
	MsgOfferEnds:      {"ينتهي العرض خلال", "Offer ends in"},
	MsgDays:           {"يوم", "days"},
	MsgHours:          {"ساعة", "hours"},
	MsgMinutes:        {"دقيقة", "minutes"},
	MsgSeconds:        {"ثانية", "seconds"},
	MsgShowMore:       {"عرض المزيد", "Show more"},
	MsgNoProducts:     {"لا توجد منتجات هنا بعد.", "No products here yet."},
	MsgNotFoundTitle:  {"الصفحة غير موجودة", "Page not found"},
	MsgNotFoundText:   {"ربما نُقلت هذه الصفحة أو لم تعد متاحة.", "This page may have moved or is no longer available."},
	MsgBackHome:       {"العودة إلى المتجر", "Back to the store"},
	MsgLanguageName:   {"العربية", "English"},
	MsgPoweredBy:      {"يعمل بواسطة iftahonline", "Powered by iftahonline"},
	MsgRatingOf:       {"التقييم %d من 5", "Rated %d out of 5"},
	MsgSkipToContent:  {"انتقل إلى المحتوى", "Skip to content"},
	MsgPolicyShipping: {"سياسة الشحن", "Shipping policy"},
	MsgPolicyRefund:   {"سياسة الاسترجاع", "Refund policy"},
	MsgPolicyPrivacy:  {"سياسة الخصوصية", "Privacy policy"},
	MsgPolicyTerms:    {"الشروط والأحكام", "Terms of service"},
	MsgBadgeShipping:  {"شحن سريع", "Fast shipping"},
	MsgBadgeCOD:       {"الدفع عند الاستلام", "Cash on delivery"},
	MsgBadgeReturns:   {"استرجاع سهل", "Easy returns"},
	MsgBadgeSecure:    {"دفع آمن", "Secure payment"},
	MsgBadgeSupport:   {"دعم العملاء", "Customer support"},
	MsgBadgeQuality:   {"جودة مضمونة", "Quality guaranteed"},
}

// T returns the message in l.
func (l Locale) T(m Msg) string {
	if l == English {
		return messages[m].en
	}
	return messages[m].ar
}

// Or returns s, or the message when s is empty: merchant text wins, the
// default fills in.
func (l Locale) Or(s string, m Msg) string {
	if s != "" {
		return s
	}
	return l.T(m)
}

// PolicyTitle is the default title of a policy.
func (l Locale) PolicyTitle(slug string) string {
	m := map[string]Msg{domain.PolicyShipping: MsgPolicyShipping, domain.PolicyRefund: MsgPolicyRefund,
		domain.PolicyPrivacy: MsgPolicyPrivacy, domain.PolicyTerms: MsgPolicyTerms}[slug]
	return l.T(m)
}

// BadgeTitle is the default text of a trust badge icon.
func (l Locale) BadgeTitle(icon string) string {
	m, ok := map[string]Msg{"shipping": MsgBadgeShipping, "cod": MsgBadgeCOD, "returns": MsgBadgeReturns,
		"secure": MsgBadgeSecure, "support": MsgBadgeSupport, "quality": MsgBadgeQuality}[icon]
	if !ok {
		return ""
	}
	return l.T(m)
}

var currencySymbols = map[Locale]map[money.Currency]string{ //nolint:gochecknoglobals // constant lookup table
	Arabic:  {money.EGP: "ج.م", money.SAR: "ر.س", money.AED: "د.إ"},
	English: {money.EGP: "EGP", money.SAR: "SAR", money.AED: "AED"},
}

// Price formats m for shoppers in l: Western digits with thousands
// separators, decimals only when there are any, and the currency in the
// language's usual form and place ("1,250 ج.م", "EGP 1,250.50").
func (l Locale) Price(m money.Money) string {
	units := m.Currency().MinorUnits()
	amount := m.Amount()
	neg := amount < 0
	if neg {
		amount = -amount
	}
	div := int64(1)
	for range units {
		div *= 10
	}
	whole, frac := amount/div, amount%div
	s := group(strconv.FormatInt(whole, 10))
	if frac != 0 {
		f := strconv.FormatInt(frac, 10)
		s += "." + strings.Repeat("0", units-len(f)) + f
	}
	if neg {
		s = "-" + s
	}
	sym := currencySymbols[l][m.Currency()]
	if sym == "" {
		sym = string(m.Currency())
	}
	if l == Arabic {
		return s + " " + sym
	}
	return sym + " " + s
}

// group inserts thousands separators into a run of digits.
func group(d string) string {
	if len(d) <= 3 {
		return d
	}
	var b strings.Builder
	first := len(d) % 3
	if first > 0 {
		b.WriteString(d[:first])
	}
	for i := first; i < len(d); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(d[i : i+3])
	}
	return b.String()
}
