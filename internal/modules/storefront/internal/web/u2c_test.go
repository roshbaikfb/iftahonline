package web_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/web"
)

func TestPieces(t *testing.T) {
	t.Parallel()
	tests := []struct {
		n      int
		ar, en string
	}{
		{1, "قطعة واحدة", "1 item"}, {2, "قطعتان", "2 items"}, {3, "3 قطع", "3 items"}, {10, "10 قطع", "10 items"}, {11, "11 قطعة", "11 items"},
	}
	for _, tt := range tests {
		if got := web.Arabic.Pieces(tt.n); got != tt.ar {
			t.Errorf("ar %d = %q, want %q", tt.n, got, tt.ar)
		}
		if got := web.English.Pieces(tt.n); got != tt.en {
			t.Errorf("en %d = %q, want %q", tt.n, got, tt.en)
		}
	}
}

// TestCartPage: cards with the product's picture, a stepper of submit
// buttons, the subtotal with its count, on the tinted background.
func TestCartPage(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	empty := f.get(t, "/cart").Body.String()
	wantContains(t, empty, `<body class="soft">`, `<div class="card-box empty-cart">`, "سلتك فارغة.")
	add := f.post(t, "/cart", url.Values{"variant": {shirtM.String()}, "qty": {"2"}}, nil, nil)
	page := f.getWith(t, "/cart", []*http.Cookie{cookie(add, "__Host-cart")}).Body.String()
	wantContains(t, page,
		`<picture class="thumb"><source type="image/avif" srcset="https://img.test/img/cotton-tee/avif/0">`,
		`<img src="https://img.test/img/cotton-tee/webp/0" width="56" height="56" alt="تيشيرت قطن"`,
		`<span>المجموع (قطعتان)</span><strong>300 ج.م</strong>`, "تظهر تكلفة الشحن عند إتمام الطلب.",
		`<a class="btn-ghost" href="/">متابعة التسوق</a>`)
}

// TestCheckoutPage: the mockup's layout; the summary counts the items and
// shows their pictures; the totals card waits for a governorate; cash on
// delivery is chosen unless the shopper picks the card.
func TestCheckoutPage(t *testing.T) {
	t.Parallel()
	f := newCheckoutFixture(t, withCards(fakePayments{on: true}))
	cookies, _ := checkoutCart(t, f)
	body := f.getWith(t, "/checkout", cookies).Body.String()
	wantContains(t, body,
		`<body class="compact soft">`, `<a class="back" href="/cart" aria-label="رجوع">`, `<strong class="title">إتمام الطلب</strong>`, `aria-label="اتصال آمن"`,
		`<details class="card-box sum">`, `ملخص الطلب (قطعتان) · <span class="show">عرض</span>`, `<picture class="thumb">`,
		"<h2>بيانات التوصيل</h2>", `<small class="hint" id="o-address-hint">الشارع، رقم العمارة، الدور، علامة مميزة</small>`,
		`placeholder="مثلاً: اتصل قبل الوصول"`,
		`<span data-amt="ship_label">الشحن</span><span data-amt="shipping">يُحسب بعد اختيار المحافظة</span>`,
		`<p class="row" data-cod-row hidden>`, `data-card-label="تأكيد الطلب والدفع"`, `>تأكيد الطلب</button>`,
	)
	if strings.Index(body, `value="card"`) > strings.Index(body, `value="cod"`) {
		t.Fatal("the card comes first, as in the mockup")
	}
}

// TestThankYouPage: the first name, never the street; what happens next
// in honest words; the delivery days of the zone; the WhatsApp question
// with the order number.
func TestThankYouPage(t *testing.T) {
	t.Parallel()
	f := newCheckoutFixture(t)
	f.content.shop.Theme.Contact.WhatsApp = "+201001234567"
	body := f.get(t, "/thank-you/IFT-1001").Body.String()
	wantContains(t, body,
		`<body class="compact soft">`, `<header class="top-p center">`,
		"<h1>شكراً يا منى، طلبك وصلنا</h1>", `<strong dir="ltr">#IFT-1001</strong>`,
		"<strong>تأكيد الطلب</strong>", "<small>سنتصل بك لتأكيد الطلب</small>", "<small>نجهّز طلبك للشحن</small>", "<small>نسلّمه لشركة الشحن</small>",
		"<small>خلال 2–3 أيام · مدينة نصر، القاهرة</small>", `<p class="where">التوصيل إلى: مدينة نصر، القاهرة</p>`,
		`<picture class="thumb">`, `href="https://wa.me/201001234567?text=`+url.QueryEscape("مرحبًا، عندي سؤال عن طلبي رقم IFT-1001")+`"`,
	)
	for _, never := range []string{"واتساب على", "أرسلنا التفاصيل", "رقم التتبع", "خلال يوم عمل"} {
		if strings.Contains(body, never) {
			t.Errorf("the page promises %q", never)
		}
	}
	// Without a number there is no WhatsApp question.
	if strings.Contains(newCheckoutFixture(t).get(t, "/thank-you/IFT-1001").Body.String(), `class="wa-ask"`) {
		t.Fatal("WhatsApp question without a number")
	}
}

// TestCheckoutStylesheet: the cart, checkout and thank-you pages link
// their own stylesheet (served by us, cached forever); the pages shoppers
// land on keep only the inlined base, for the first paint.
func TestCheckoutStylesheet(t *testing.T) {
	t.Parallel()
	f := newCheckoutFixture(t)
	cookies, _ := checkoutCart(t, f)
	link := regexp.MustCompile(`<link rel="stylesheet" href="(/assets/checkout\.[0-9a-f]{8}\.css)">`)
	var href string
	for _, page := range []string{f.getWith(t, "/cart", cookies).Body.String(), f.getWith(t, "/checkout", cookies).Body.String(), f.get(t, "/thank-you/IFT-1001").Body.String()} {
		m := link.FindStringSubmatch(page)
		if m == nil {
			t.Fatal("a cart page without its stylesheet")
		}
		href = m[1]
	}
	for _, path := range []string{"/", "/p/cotton-tee", "/c/tshirts"} {
		if strings.Contains(f.get(t, path).Body.String(), `rel="stylesheet"`) {
			t.Errorf("%s links a stylesheet", path)
		}
	}
	a, err := web.LoadAssets()
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, href, nil))
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/css") ||
		!strings.Contains(rec.Header().Get("Cache-Control"), "immutable") || !strings.Contains(rec.Body.String(), ".card-box") {
		t.Fatalf("checkout.css: %d %v", rec.Code, rec.Header())
	}
}
