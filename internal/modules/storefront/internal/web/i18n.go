package web

import (
	"encoding/json"
	"fmt"
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
	MsgCart
	MsgCartEmpty
	MsgSubtotal
	MsgUpdate
	MsgRemove
	MsgCheckout
	MsgContinueShopping
	MsgLineUnavailable
	MsgErrUnavailable
	MsgErrStock
	MsgErrCartFull
	MsgErrQty
	MsgErrBusy
	MsgThankYou
	MsgThankYouText
	MsgOrderRef
	MsgTotal
	MsgYourName
	MsgPhone
	MsgGovernorate
	MsgChooseGovernorate
	MsgCity
	MsgAddress
	MsgNotes
	MsgOptional
	MsgPlaceOrder
	MsgPayOnDelivery
	MsgOrderSummary
	MsgVerifyHuman
	MsgErrFix
	MsgErrName
	MsgErrPhone
	MsgErrGovernorate
	MsgErrCity
	MsgErrAddress
	MsgErrNotes
	MsgErrOrderRefused
	MsgErrCaptcha
	MsgErrCartChanged
	MsgShipping
	MsgShippingFree
	MsgCODFee
	MsgErrNotDelivered
	MsgPaymentMethod
	MsgPayCash
	MsgPayCard
	MsgPayCardNote
	MsgPayByCardCTA
	MsgPaid
	MsgPaymentPending
	MsgPayNow
	MsgPaymentExpired
	MsgErrCardUnavailable
	MsgMenu
	MsgShopNow
	MsgShopByCategory
	MsgAll
	MsgSalePercent
	MsgSoldOutShort
	MsgViewAll
	MsgHelp
	MsgContactUs
	MsgPaymentMethods
	MsgWhatsAppHelp
	MsgWhatsAppHelpText
	MsgContact
	MsgWhatsApp
	MsgEmail
	MsgDeliveryTo
	MsgShippingCost
	MsgShippingFreeLine
	MsgFreeOver
	MsgBack
	MsgShare
	MsgLinkCopied
	MsgYouMayLike
	MsgSave
	MsgLeftOne
	MsgLeftTwo
	MsgLeftMany
	MsgDeliveryPrompt
	MsgShippingAtCheckout
	MsgPayCODOrCard
	MsgPayCardSecure
	MsgPayCODOnly
	MsgPayCODNote
	MsgReturns
	MsgReadPolicy
	MsgOrderViaWhatsApp
	MsgWhatsAppOrder
	MsgLess
	MsgMore
	MsgImageN
	msgCount
)

// messages holds each message in both languages side by side, so a
// translation cannot go missing in one of them.
var messages = [msgCount]struct{ ar, en string }{ //nolint:gochecknoglobals // constant translation table
	MsgHome:              {"الرئيسية", "Home"},
	MsgAddToCart:         {"أضف إلى السلة", "Add to cart"},
	MsgOrderNow:          {"اطلب الآن", "Order now"},
	MsgSoldOut:           {"نفدت الكمية", "Sold out"},
	MsgOptions:           {"الاختيار", "Option"},
	MsgQuantity:          {"الكمية", "Quantity"},
	MsgDescription:       {"الوصف", "Description"},
	MsgNewArrivals:       {"وصل حديثًا", "New arrivals"},
	MsgProducts:          {"المنتجات", "Products"},
	MsgReviews:           {"آراء العملاء", "Customer reviews"},
	MsgFAQ:               {"الأسئلة الشائعة", "Frequently asked questions"},
	MsgOfferEnds:         {"ينتهي العرض خلال", "Offer ends in"},
	MsgDays:              {"يوم", "days"},
	MsgHours:             {"ساعة", "hours"},
	MsgMinutes:           {"دقيقة", "minutes"},
	MsgSeconds:           {"ثانية", "seconds"},
	MsgShowMore:          {"عرض المزيد", "Show more"},
	MsgNoProducts:        {"لا توجد منتجات هنا بعد.", "No products here yet."},
	MsgNotFoundTitle:     {"الصفحة غير موجودة", "Page not found"},
	MsgNotFoundText:      {"ربما نُقلت هذه الصفحة أو لم تعد متاحة.", "This page may have moved or is no longer available."},
	MsgBackHome:          {"العودة إلى المتجر", "Back to the store"},
	MsgLanguageName:      {"العربية", "English"},
	MsgPoweredBy:         {"بمنصة افتح أونلاين", "Powered by iftahonline"},
	MsgRatingOf:          {"التقييم %d من 5", "Rated %d out of 5"},
	MsgSkipToContent:     {"انتقل إلى المحتوى", "Skip to content"},
	MsgPolicyShipping:    {"سياسة الشحن", "Shipping policy"},
	MsgPolicyRefund:      {"سياسة الاسترجاع", "Refund policy"},
	MsgPolicyPrivacy:     {"سياسة الخصوصية", "Privacy policy"},
	MsgPolicyTerms:       {"الشروط والأحكام", "Terms of service"},
	MsgBadgeShipping:     {"شحن سريع", "Fast shipping"},
	MsgBadgeCOD:          {"الدفع عند الاستلام", "Cash on delivery"},
	MsgBadgeReturns:      {"استرجاع سهل", "Easy returns"},
	MsgBadgeSecure:       {"دفع آمن", "Secure payment"},
	MsgBadgeSupport:      {"دعم العملاء", "Customer support"},
	MsgBadgeQuality:      {"جودة مضمونة", "Quality guaranteed"},
	MsgCart:              {"السلة", "Cart"},
	MsgCartEmpty:         {"سلتك فارغة.", "Your cart is empty."},
	MsgSubtotal:          {"المجموع", "Subtotal"},
	MsgUpdate:            {"تحديث", "Update"},
	MsgRemove:            {"حذف", "Remove"},
	MsgCheckout:          {"إتمام الطلب", "Checkout"},
	MsgContinueShopping:  {"متابعة التسوق", "Continue shopping"},
	MsgLineUnavailable:   {"غير متوفر بهذه الكمية", "Not available in this quantity"},
	MsgErrUnavailable:    {"هذا المنتج لم يعد متاحًا.", "This item is no longer available."},
	MsgErrStock:          {"الكمية المطلوبة غير متوفرة.", "Not enough of this item is left."},
	MsgErrCartFull:       {"السلة ممتلئة؛ احذف بعض المنتجات أولًا.", "Your cart is full; remove some items first."},
	MsgErrQty:            {"اختر كمية من 1 إلى 20.", "Choose a quantity from 1 to 20."},
	MsgErrBusy:           {"طلبات كثيرة؛ حاول مرة أخرى بعد قليل.", "Too many requests; please try again shortly."},
	MsgThankYou:          {"شكرًا لطلبك!", "Thank you for your order!"},
	MsgThankYouText:      {"استلمنا طلبك وسنتواصل معك قريبًا.", "We received your order and will be in touch soon."},
	MsgOrderRef:          {"رقم الطلب", "Order number"},
	MsgTotal:             {"الإجمالي", "Total"},
	MsgYourName:          {"الاسم", "Name"},
	MsgPhone:             {"رقم الموبايل", "Mobile number"},
	MsgGovernorate:       {"المحافظة", "Governorate"},
	MsgChooseGovernorate: {"اختر المحافظة", "Choose a governorate"},
	MsgCity:              {"المدينة / المنطقة", "City / area"},
	MsgAddress:           {"العنوان بالتفصيل", "Full address"},
	MsgNotes:             {"ملاحظات", "Notes"},
	MsgOptional:          {"اختياري", "optional"},
	MsgPlaceOrder:        {"تأكيد الطلب", "Place order"},
	MsgPayOnDelivery:     {"الدفع نقدًا عند الاستلام. سنتصل بك لتأكيد الطلب.", "Pay cash on delivery. We will call you to confirm."},
	MsgOrderSummary:      {"ملخص الطلب", "Order summary"},
	MsgVerifyHuman:       {"للمتابعة، أكّد أنك لست روبوتًا ثم أرسل الطلب مرة أخرى.", "To continue, confirm you are not a robot, then send the order again."},
	MsgErrFix:            {"راجع البيانات المظللة.", "Please check the highlighted fields."},
	MsgErrName:           {"اكتب اسمك (حرفان على الأقل).", "Enter your name (at least 2 letters)."},
	MsgErrPhone:          {"اكتب رقم موبايل صحيح.", "Enter a valid mobile number."},
	MsgErrGovernorate:    {"اختر المحافظة من القائمة.", "Choose your governorate from the list."},
	MsgErrCity:           {"اكتب المدينة أو المنطقة.", "Enter your city or area."},
	MsgErrAddress:        {"اكتب العنوان بالتفصيل (5 أحرف على الأقل).", "Enter your full address (at least 5 characters)."},
	MsgErrNotes:          {"الملاحظات طويلة جدًا.", "Your notes are too long."},
	MsgErrOrderRefused:   {"تعذّر إتمام الطلب. تواصل مع المتجر.", "We could not place this order. Please contact the store."},
	MsgErrCaptcha:        {"لم نتمكن من التحقق. حاول مرة أخرى.", "We could not verify you. Please try again."},
	MsgErrCartChanged:    {"تغيّرت سلتك؛ راجعها قبل إتمام الطلب.", "Your cart changed; please review it before checking out."},
	MsgShipping:          {"الشحن", "Shipping"},
	MsgShippingFree:      {"مجاني", "Free"},
	MsgCODFee:            {"رسوم الدفع عند الاستلام", "Cash-on-delivery fee"},
	MsgErrNotDelivered:   {"لا نوصّل إلى هذه المحافظة حاليًا.", "We do not deliver to this governorate yet."},
	MsgPaymentMethod:     {"طريقة الدفع", "Payment method"},
	MsgPayCash:           {"نقدًا عند الاستلام", "Cash on delivery"},
	MsgPayCard:           {"بطاقة بنكية (أونلاين)", "Card (online)"},
	MsgPayCardNote:       {"ستنتقل إلى صفحة الدفع الآمنة لإتمام الدفع. لا نرى بيانات بطاقتك.", "You will pay on the secure payment page. We never see your card."},
	MsgPayByCardCTA:      {"تأكيد الطلب والدفع", "Place order and pay"},
	MsgPaid:              {"تم الدفع بنجاح.", "Your payment was received."},
	MsgPaymentPending: {"لم يصلنا الدفع بعد. إذا دفعت للتو فسيتأكد خلال لحظات (تتحدّث الصفحة تلقائيًا)، وإلا فادفع الآن.",
		"We have not received your payment yet. If you just paid, it will be confirmed in a moment (this page updates itself); otherwise, pay now."},
	MsgPayNow:             {"ادفع الآن", "Pay now"},
	MsgPaymentExpired:     {"انتهت مهلة الدفع وأُلغي الطلب. يمكنك الطلب من جديد.", "The payment window passed and the order was cancelled. You can order again."},
	MsgErrCardUnavailable: {"الدفع بالبطاقة غير متاح حاليًا؛ اختر الدفع عند الاستلام.", "Card payment is not available right now; please choose cash on delivery."},
	MsgMenu:               {"القائمة", "Menu"},
	MsgShopNow:            {"تسوّق الآن", "Shop now"},
	MsgShopByCategory:     {"تسوّق حسب التصنيف", "Shop by category"},
	MsgAll:                {"الكل", "All"},
	MsgSalePercent:        {"خصم %d%%", "%d%% off"},
	MsgSoldOutShort:       {"نفد", "Sold out"},
	MsgViewAll:            {"عرض الكل", "View all"},
	MsgHelp:               {"المساعدة", "Help"},
	MsgContactUs:          {"تواصل معنا", "Contact us"},
	MsgPaymentMethods:     {"طرق الدفع", "Payment methods"},
	MsgWhatsAppHelp:       {"محتاج مساعدة في الطلب؟", "Need help with your order?"},
	MsgWhatsAppHelpText:   {"كلّمنا على واتساب ونرد عليك بسرعة", "Message us on WhatsApp and we will reply quickly"},
	MsgContact:            {"تواصل", "Chat"},
	MsgWhatsApp:           {"واتساب", "WhatsApp"},
	MsgEmail:              {"البريد", "Email"},
	MsgDeliveryTo:         {"التوصيل إلى", "Delivery to"},
	MsgShippingCost:       {"الشحن", "Shipping"},
	MsgShippingFreeLine:   {"الشحن مجاني", "Free shipping"},
	MsgFreeOver:           {"مجاني فوق", "free over"},
	MsgBack:               {"رجوع", "Back"},
	MsgShare:              {"مشاركة", "Share"},
	MsgLinkCopied:         {"تم نسخ الرابط", "Link copied"},
	MsgYouMayLike:         {"قد يعجبك أيضاً", "You may also like"},
	MsgSave:               {"وفّر %s", "Save %s"},
	MsgLeftOne:            {"باقي قطعة واحدة فقط", "Only 1 left"},
	MsgLeftTwo:            {"باقي قطعتان فقط", "Only 2 left"},
	MsgLeftMany:           {"باقي %d قطع فقط", "Only %d left"},
	MsgDeliveryPrompt:     {"اختر محافظتك لمعرفة الشحن وموعد التوصيل", "Choose your governorate to see shipping and delivery time"},
	MsgShippingAtCheckout: {"تظهر تكلفة الشحن عند إتمام الطلب.", "Shipping is shown at checkout."},
	MsgPayCODOrCard:       {"ادفع عند الاستلام أو بالبطاقة", "Pay on delivery or by card"},
	MsgPayCardSecure:      {"الدفع بالبطاقة على صفحة دفع آمنة", "Card payments on a secure payment page"},
	MsgPayCODOnly:         {"الدفع عند الاستلام", "Cash on delivery"},
	MsgPayCODNote:         {"تدفع نقدًا للمندوب عند استلام طلبك", "Pay the courier in cash when your order arrives"},
	MsgReturns:            {"الاستبدال والاسترجاع", "Exchanges and returns"},
	MsgReadPolicy:         {"اقرأ السياسة", "Read the policy"},
	MsgOrderViaWhatsApp:   {"اطلب عبر واتساب", "Order on WhatsApp"},
	MsgWhatsAppOrder:      {"مرحبًا، أريد طلب:", "Hello, I would like to order:"},
	MsgLess:               {"أقل", "Fewer"},
	MsgMore:               {"أكثر", "More"},
	MsgImageN:             {"صورة %d", "Image %d"},
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

// DeliveryDays says how long delivery takes: "خلال 2–3 أيام", "in 2–3
// days", "في نفس اليوم". Arabic counts the way it is said: يوم، يومين،
// 3 أيام، 11 يومًا.
func (l Locale) DeliveryDays(minDays, maxDays int) string {
	if l == English {
		switch {
		case maxDays == 0:
			return "the same day"
		case minDays == maxDays && maxDays == 1:
			return "in 1 day"
		case minDays == maxDays:
			return "in " + strconv.Itoa(maxDays) + " days"
		}
		return "in " + strconv.Itoa(minDays) + "–" + strconv.Itoa(maxDays) + " days"
	}
	unit := "أيام"
	if maxDays > 10 {
		unit = "يومًا"
	}
	switch {
	case maxDays == 0:
		return "في نفس اليوم"
	case minDays == maxDays && maxDays == 1:
		return "خلال يوم"
	case minDays == maxDays && maxDays == 2:
		return "خلال يومين"
	case minDays == maxDays:
		return "خلال " + strconv.Itoa(maxDays) + " " + unit
	}
	return "خلال " + strconv.Itoa(minDays) + "–" + strconv.Itoa(maxDays) + " " + unit
}

// Left is "only N left" for 1 to 10 units, the way each language says it.
func (l Locale) Left(n int) string {
	switch n {
	case 1:
		return l.T(MsgLeftOne)
	case 2:
		return l.T(MsgLeftTwo)
	}
	return fmt.Sprintf(l.T(MsgLeftMany), n)
}

// moneyFormat is how the page's script writes amounts it computes (a
// total for a quantity) exactly as Price does: the symbol, its side and
// the currency's minor units.
func (l Locale) moneyFormat(c money.Currency) string {
	sym := currencySymbols[l][c]
	if sym == "" {
		sym = string(c)
	}
	b, _ := json.Marshal(struct { //nolint:errcheck,errchkjson // fixed types always encode
		Sym   string `json:"sym"`
		After bool   `json:"after"`
		Units int    `json:"units"`
	}{sym, l == Arabic, c.MinorUnits()})
	return string(b)
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
