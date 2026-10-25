package web_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/web"
)

func quoteOf(t *testing.T, f checkoutFixture, path string, form url.Values, cookies []*http.Cookie) (int, map[string]any) {
	t.Helper()
	rec := f.post(t, path, form, cookies, nil)
	var q map[string]any
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &q); err != nil {
			t.Fatalf("quote body %q: %v", rec.Body, err)
		}
		if !strings.Contains(rec.Header().Get("Cache-Control"), "no-store") {
			t.Fatalf("a quote must never be cached: %q", rec.Header().Get("Cache-Control"))
		}
	}
	return rec.Code, q
}

// TestOrderQuote: the order form's quote is one line in the page's
// language, of the landing form's variant or of the cart; it says when
// the store does not deliver, and stays silent on anything it cannot
// price yet.
func TestOrderQuote(t *testing.T) {
	t.Parallel()
	f := newCheckoutFixture(t)
	v := f.shirt.Variants[0].ID.String()
	form := func(gov, qty string) url.Values {
		return url.Values{"variant": {v}, "qty": {qty}, "governorate": {gov}, "city": {"مدينة نصر"}}
	}
	tests := []struct {
		name, path string
		form       url.Values
		text       string
		ok         bool
	}{
		{"zone rate and fee", "/order/quote", form("EG-C", "2"), "الشحن: 50 ج.م · رسوم الدفع عند الاستلام: 10 ج.م · الإجمالي: 360 ج.م", true},
		{"free shipping", "/order/quote", form("EG-C", "7"), "الشحن: مجاني · رسوم الدفع عند الاستلام: 10 ج.م · الإجمالي: 1,060 ج.م", true},
		{"English", "/en/order/quote", form("EG-C", "2"), "Shipping: EGP 50 · Cash-on-delivery fee: EGP 10 · Total: EGP 360", true},
		{"not delivered", "/order/quote", form("EG-ASN", "2"), "لا نوصّل إلى هذه المحافظة حاليًا.", false},
		{"no governorate yet", "/order/quote", form("", "2"), "", true},
		{"invalid governorate", "/order/quote", form("EG-XX", "2"), "", true},
		{"bad quantity", "/order/quote", form("EG-C", "0"), "", true},
		{"bad variant", "/order/quote", url.Values{"variant": {"nope"}, "qty": {"1"}, "governorate": {"EG-C"}}, "", true},
		{"empty cart", "/order/quote", url.Values{"governorate": {"EG-C"}}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, q := quoteOf(t, f, tt.path, tt.form, nil)
			if code != http.StatusOK || q["text"] != tt.text || q["deliverable"] != tt.ok {
				t.Fatalf("%d %v; want %q deliverable=%v", code, q, tt.text, tt.ok)
			}
		})
	}

	// Checkout's quote is of the cart.
	add := f.post(t, "/cart", url.Values{"variant": {v}, "qty": {"3"}}, nil, nil)
	cookies := []*http.Cookie{cookie(add, "__Host-cart")}
	if code, q := quoteOf(t, f, "/order/quote", url.Values{"governorate": {"EG-C"}}, cookies); code != http.StatusOK ||
		q["text"] != "الشحن: 50 ج.م · رسوم الدفع عند الاستلام: 10 ج.م · الإجمالي: 510 ج.م" {
		t.Fatalf("cart quote: %d %v", code, q)
	}

	if rec := f.post(t, "/order/quote", form("EG-C", "1"), nil, map[string]string{"Origin": "https://evil.example"}); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site quote: %d", rec.Code)
	}
	if rec := f.post(t, "/order/quote", form("EG-LX", "1"), nil, nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("orders down: %d", rec.Code)
	}
	if len(f.orders.placed()) != 0 {
		t.Fatal("a quote placed an order")
	}
}

// TestProductDeliveryLines: the product page's delivery box asks the
// same quote and shows two lines: where and how long, then what
// shipping costs and from when it is free.
func TestProductDeliveryLines(t *testing.T) {
	t.Parallel()
	f := newCheckoutFixture(t)
	v := f.shirt.Variants[0].ID.String()
	form := func(gov, qty string) url.Values {
		return url.Values{"variant": {v}, "qty": {qty}, "governorate": {gov}}
	}
	tests := []struct {
		name, path     string
		form           url.Values
		delivery, ship string
	}{
		{"days and threshold", "/order/quote", form("EG-C", "1"), "التوصيل إلى القاهرة خلال 2–3 أيام", "الشحن 50 ج.م · مجاني فوق 1,000 ج.م"},
		{"free reached", "/order/quote", form("EG-C", "7"), "التوصيل إلى القاهرة خلال 2–3 أيام", "الشحن مجاني"},
		{"no days said", "/order/quote", form("EG-GZ", "1"), "التوصيل إلى الجيزة", "الشحن 50 ج.م · مجاني فوق 1,000 ج.م"},
		{"English", "/en/order/quote", form("EG-C", "1"), "Delivery to Cairo in 2–3 days", "Shipping EGP 50 · free over EGP 1,000"},
		{"not delivered", "/order/quote", form("EG-ASN", "1"), "لا نوصّل إلى هذه المحافظة حاليًا.", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, q := quoteOf(t, f, tt.path, tt.form, nil)
			ship, _ := q["shipping"].(string) // absent reads as ""
			if code != http.StatusOK || q["delivery"] != tt.delivery || ship != tt.ship {
				t.Fatalf("%d %v; want %q / %q", code, q, tt.delivery, tt.ship)
			}
		})
	}
}

// TestCheckoutAmounts: checkout's totals card gets each amount on its
// own line, and the cash-on-delivery choice its fee.
func TestCheckoutAmounts(t *testing.T) {
	t.Parallel()
	f := newCheckoutFixture(t)
	add := f.post(t, "/cart", url.Values{"variant": {f.shirt.Variants[0].ID.String()}, "qty": {"2"}}, nil, nil)
	cookies := []*http.Cookie{cookie(add, "__Host-cart")}
	_, q := quoteOf(t, f, "/order/quote", url.Values{"governorate": {"EG-C"}}, cookies)
	want := map[string]any{"subtotal": "300 ج.م", "ship_label": "الشحن إلى القاهرة", "shipping": "50 ج.م", "cod_fee": "10 ج.م",
		"total": "360 ج.م", "cod_note": "نقداً للمندوب · رسوم 10 ج.م"}
	got, _ := q["amounts"].(map[string]any)
	for k, v := range want {
		if got[k] != v {
			t.Errorf("amounts[%s] = %v, want %q", k, got[k], v)
		}
	}
	// By card: no fee, and no cash note to change.
	_, q = quoteOf(t, f, "/order/quote", url.Values{"governorate": {"EG-C"}, "pay_method": {"card"}}, cookies)
	if got, _ = q["amounts"].(map[string]any); got["cod_fee"] != nil || got["total"] != "350 ج.م" || got["cod_note"] != nil {
		t.Fatalf("by card: %v", got)
	}
	// Free shipping reads "مجاني".
	big := f.post(t, "/cart", url.Values{"variant": {f.shirt.Variants[0].ID.String()}, "qty": {"7"}}, nil, nil)
	_, q = quoteOf(t, f, "/order/quote", url.Values{"governorate": {"EG-C"}}, []*http.Cookie{cookie(big, "__Host-cart")})
	if got, _ = q["amounts"].(map[string]any); got["shipping"] != "مجاني" {
		t.Fatalf("free shipping: %v", got)
	}
	// Not delivered: no amounts.
	if _, q = quoteOf(t, f, "/order/quote", url.Values{"governorate": {"EG-ASN"}}, cookies); q["amounts"] != nil {
		t.Fatalf("not delivered: %v", q)
	}
}

func TestDeliveryDaysWording(t *testing.T) {
	t.Parallel()
	tests := []struct {
		min, max int
		ar, en   string
	}{
		{0, 0, "في نفس اليوم", "the same day"},
		{1, 1, "خلال يوم", "in 1 day"},
		{2, 2, "خلال يومين", "in 2 days"},
		{3, 3, "خلال 3 أيام", "in 3 days"},
		{1, 2, "خلال 1–2 أيام", "in 1–2 days"},
		{2, 3, "خلال 2–3 أيام", "in 2–3 days"},
		{7, 14, "خلال 7–14 يومًا", "in 7–14 days"},
		{11, 11, "خلال 11 يومًا", "in 11 days"},
	}
	for _, tt := range tests {
		if got := web.Arabic.DeliveryDays(tt.min, tt.max); got != tt.ar {
			t.Errorf("ar %d–%d = %q, want %q", tt.min, tt.max, got, tt.ar)
		}
		if got := web.English.DeliveryDays(tt.min, tt.max); got != tt.en {
			t.Errorf("en %d–%d = %q, want %q", tt.min, tt.max, got, tt.en)
		}
	}
}

func TestOrderQuoteLimits(t *testing.T) {
	t.Parallel()
	f := newCheckoutFixture(t)
	f.limiter.allow["quote:ip:203.0.113.7"] = 2
	form := url.Values{"variant": {f.shirt.Variants[0].ID.String()}, "qty": {"1"}, "governorate": {"EG-C"}}
	for range 2 {
		if code, _ := quoteOf(t, f, "/order/quote", form, nil); code != http.StatusOK {
			t.Fatal(code)
		}
	}
	if rec := f.post(t, "/order/quote", form, nil, nil); rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("over the limit: %d %v", rec.Code, rec.Header())
	}
	// A limiter outage still answers: a quote changes nothing.
	f.limiter.fail = true
	if code, q := quoteOf(t, f, "/order/quote", form, nil); code != http.StatusOK || q["text"] == "" {
		t.Fatalf("limiter down: %d %v", code, q)
	}
	// Without the orders module there is no quote.
	if rec := newFixture(t).post(t, "/order/quote", form, nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("no orders: %d", rec.Code)
	}
}

// TestNotDeliveredOnSubmit: placing an order outside the zones shows the
// reason under the governorate.
func TestNotDeliveredOnSubmit(t *testing.T) {
	t.Parallel()
	f := newCheckoutFixture(t)
	f.orders.log.err = &web.OrderInvalidError{Fields: []string{"governorate"}, NotDelivered: true}
	rec := f.post(t, "/order", f.orderForm(), nil, nil)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "لا نوصّل إلى هذه المحافظة حاليًا.") {
		t.Fatalf("not delivered: %d", rec.Code)
	}
}

func TestThankYouShowsCharges(t *testing.T) {
	t.Parallel()
	f := newCheckoutFixture(t)
	wantContains(t, f.get(t, "/thank-you/IFT-1001").Body.String(),
		"<span>المجموع</span><span>300 ج.م</span>", "<span>الشحن</span><span>50 ج.م</span>",
		"<span>رسوم الدفع عند الاستلام</span><span>10 ج.م</span>", "<span>الإجمالي</span><strong>360 ج.م</strong>")
	wantContains(t, f.get(t, "/pages/offer").Body.String(), `data-quote="/order/quote"`, `aria-live="polite"`)
}
