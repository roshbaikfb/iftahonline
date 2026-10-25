package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/web"
)

func intp(v int) *int { return &v }

// TestLowStockLine: "only N left" shows within the store's threshold
// (5 unless it chose), never above it, and not at all when it is off;
// the script's copy of the variants carries no count the page would not
// show.
func TestLowStockLine(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		left      int
		threshold *int
		want      string
	}{
		{"three left, default threshold", 3, nil, "باقي 3 قطع فقط"},
		{"one left", 1, nil, "باقي قطعة واحدة فقط"},
		{"two left", 2, nil, "باقي قطعتان فقط"},
		{"above the threshold", 7, nil, ""},
		{"the store's own threshold", 7, intp(8), "باقي 7 قطع فقط"},
		{"turned off", 1, intp(0), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.catalog.products[0].Variants[0].Left = tt.left
			f.content.shop.Theme.LowStock = tt.threshold
			body := f.get(t, "/p/cotton-tee").Body.String()
			if tt.want == "" {
				if !strings.Contains(body, `<p class="left" data-left hidden></p>`) || strings.Contains(body, "باقي") {
					t.Fatal("a count the store does not announce is on the page")
				}
				return
			}
			wantContains(t, body, `<p class="left" data-left>`+tt.want+`</p>`, `&#34;left&#34;:&#34;`+tt.want+`&#34;`)
		})
	}
}

// TestLowStockFitsCatalog: the store's threshold can never ask for more
// than the catalog tells the storefront.
func TestLowStockFitsCatalog(t *testing.T) {
	t.Parallel()
	if domain.MaxLowStock > catalog.MaxShownLeft {
		t.Fatalf("threshold up to %d, the catalog reports up to %d", domain.MaxLowStock, catalog.MaxShownLeft)
	}
}

func TestColorSwatches(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	tee := product("بولو", "polo", 32000, true, f.cat.ID)
	tee.Options = []catalog.Option{{Name: "المقاس", Values: []string{"M", "L"}}, {Name: "اللون", Values: []string{"كحلي", "أبيض", "تركواز غامق"}}}
	tee.Variants = []catalog.StorefrontVariant{
		{ID: gen.New(), OptionValues: []string{"M", "كحلي"}, Price: egp(32000), Available: true},
		{ID: gen.New(), OptionValues: []string{"L", "أبيض"}, Price: egp(32000), Available: true},
		{ID: gen.New(), OptionValues: []string{"L", "تركواز غامق"}, Price: egp(32000), Available: false},
	}
	f.catalog.products = append(f.catalog.products, tee)
	body := f.get(t, "/p/polo").Body.String()
	wantContains(t, body,
		`<fieldset class="opt colors"><legend>اللون: <strong data-chosen="1">كحلي</strong></legend>`,
		`<label class="sw sw-navy"><input type="radio" name="o1" value="كحلي" checked`, `<span class="sr">كحلي</span>`,
		`<label class="sw sw-white"><input type="radio" name="o1" value="أبيض"`,
		// an unknown color is a text button; no variant of it can be bought
		`<label class="out"><input type="radio" name="o1" value="تركواز غامق" disabled`, `<span>تركواز غامق</span>`,
	)
}

func TestProductInfoBox(t *testing.T) {
	t.Parallel()
	t.Run("delivery asks the quote; cards; returns policy", func(t *testing.T) {
		t.Parallel()
		f := newCheckoutFixture(t, withCards(fakePayments{on: true}))
		body := f.get(t, "/p/cotton-tee").Body.String()
		wantContains(t, body,
			`<form class="deliv" data-delivery="/order/quote">`, `<label for="deliv-gov">اختر محافظتك لمعرفة الشحن وموعد التوصيل</label>`,
			`<option value="">اختر المحافظة</option>`, `<option value="EG-C">القاهرة</option>`,
			"<strong>ادفع عند الاستلام أو بالبطاقة</strong>", "<small>الدفع بالبطاقة على صفحة دفع آمنة</small>",
			"<strong>الاستبدال والاسترجاع</strong>", `<a href="/policies/refund">اقرأ السياسة</a>`,
		)
		if strings.Contains(body, `selected`) && strings.Contains(body, `value="EG-C" selected`) {
			t.Fatal("the page must not assume a governorate")
		}
	})
	t.Run("no orders, no cards, no policy", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.content.shop.Policies = nil
		body := f.get(t, "/p/cotton-tee").Body.String()
		wantContains(t, body, "<small>تظهر تكلفة الشحن عند إتمام الطلب.</small>", "<strong>الدفع عند الاستلام</strong>")
		if strings.Contains(body, "data-delivery=") || strings.Contains(body, "اقرأ السياسة") || strings.Contains(body, "بالبطاقة") {
			t.Fatal("the box promises what the store does not offer")
		}
	})
}

func TestWhatsAppOrder(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	body := f.get(t, "/p/cotton-tee").Body.String()
	if strings.Contains(body, `class="wa-order"`) {
		t.Fatal("WhatsApp button without a number")
	}
	f = newFixture(t)
	f.content.shop.Theme.Contact.WhatsApp = "+201001234567"
	body = f.get(t, "/p/cotton-tee").Body.String()
	text := url.QueryEscape("مرحبًا، أريد طلب: تيشيرت قطن (M) × 1\nhttps://mona.iftahonline.com/p/cotton-tee")
	wantContains(t, body, `<a class="wa-order" href="https://wa.me/201001234567?text=`+strings.ReplaceAll(text, "&", "&amp;")+`"`,
		`aria-label="اطلب عبر واتساب"`)
}

func TestRelatedProducts(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for _, slug := range []string{"a", "b", "c", "d", "e"} {
		f.catalog.products = append(f.catalog.products, product("منتج "+slug, "rel-"+slug, 10000, true, f.cat.ID))
	}
	rec := f.get(t, "/p/cotton-tee")
	body := rec.Body.String()
	wantContains(t, body, "<h2>قد يعجبك أيضاً</h2>", `<div class="grid row">`)
	if n := strings.Count(body, `<div class="card">`); n != 4 {
		t.Fatalf("%d related products, want 4 (never the product itself)", n)
	}
	if strings.Count(body, `href="/p/cotton-tee"`) > 0 && strings.Contains(body, `<a class="name" href="/p/cotton-tee">`) {
		t.Fatal("the product is among its own related products")
	}
	if c := f.catalog.calls0("list"); c != 1 {
		t.Fatalf("%d list queries, want 1", c)
	}
	if tags := rec.Header().Get("Cache-Tag"); strings.Contains(tags, "l-") {
		t.Fatalf("Cache-Tag %q: a product page is not a listing (it is renewed by its related products' own tags)", tags)
	}
	// A product in no category asks for nothing more.
	f2 := newFixture(t)
	f2.get(t, "/p/cap")
	if c := f2.catalog.calls0("list"); c != 0 {
		t.Fatalf("%d list queries for a product in no category", c)
	}
}

func TestProductHeaderOnPhones(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	body := f.get(t, "/p/cotton-tee").Body.String()
	wantContains(t, body, `<body class="compact">`, `<header class="top-p">`,
		`<a class="back" href="/c/tshirts" aria-label="رجوع">`,
		`data-share="https://mona.iftahonline.com/p/cotton-tee"`, `aria-label="مشاركة" hidden>`,
		// the short header has no language switch: the footer has one on every page
		`<a href="/en/p/cotton-tee" hreflang="en" lang="en">English</a></p></footer>`)
	// a product in no category goes back home; other pages keep the full header only
	wantContains(t, f.get(t, "/p/cap").Body.String(), `<a class="back" href="/" aria-label="رجوع">`)
	if strings.Contains(f.get(t, "/").Body.String(), `class="top-p"`) {
		t.Fatal("the short header is for the product page")
	}
}

func TestSoldOutProduct(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	body := f.get(t, "/p/cap").Body.String()
	wantContains(t, body, `<span class="tag out">نفد</span>`,
		`<button class="btn btn-block add-inline" type="submit" disabled data-add>نفدت الكمية</button>`,
		`form="buy" disabled data-add data-total>نفدت الكمية</button>`)
}

func TestEnglishProductPage(t *testing.T) {
	t.Parallel()
	f := newCheckoutFixture(t)
	f.catalog.products[0].Variants[0].Left = 2
	body := f.get(t, "/en/p/cotton-tee").Body.String()
	wantContains(t, body, `<span class="now sale" data-now>EGP 150</span>`, "Save EGP 50", "Only 2 left",
		"Choose your governorate to see shipping and delivery time", `<option value="EG-C">Cairo</option>`,
		`&#34;sym&#34;:&#34;EGP&#34;,&#34;after&#34;:false`)
	_ = web.English
	_ = http.StatusOK
}
