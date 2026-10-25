package web_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/web"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
)

// withContact gives the fixture's theme the texts and contact a merchant
// fills in.
func withContact(f fixture) {
	f.content.mu.Lock()
	defer f.content.mu.Unlock()
	th := &f.content.shop.Theme
	th.Announcement = "شحن مجاني فوق 1,000 ج.م"
	th.About = "ملابس قطن مصري"
	th.Contact = domain.Contact{WhatsApp: "+201001234567", Email: "hi@mona.test"}
}

func TestLayoutHeaderAndFooter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		opts      []func(*web.Deps)
		contact   bool
		wants     []string
		notWanted []string
	}{
		{
			name: "bare store",
			wants: []string{
				`<details class="menu">`, `<span class="sr">القائمة</span>`, // the phone menu, with the categories
				`<nav class="wrap cats"`, `<a href="/" aria-current="page">الرئيسية</a>`, `<a href="/c/tshirts">تيشيرتات</a>`,
				`class="cartlink" href="/cart"`, `<span class="lbl">السلة</span>`, `data-cart-count hidden`,
				"<h2>المساعدة</h2>", `href="/policies/refund"`,
				"<h2>طرق الدفع</h2>", "<li>الدفع عند الاستلام</li>",
				"© متجر منى · بمنصة افتح أونلاين",
			},
			notWanted: []string{`class="announce"`, "<h2>تواصل معنا</h2>", "Visa", `class="wa"`},
		},
		{
			name:    "contact, texts and cards",
			opts:    []func(*web.Deps){withCards(fakePayments{on: true})},
			contact: true,
			wants: []string{
				`<div class="announce">شحن مجاني فوق 1,000 ج.م</div>`, "<p>ملابس قطن مصري</p>",
				"<h2>تواصل معنا</h2>", `<a href="https://wa.me/201001234567" dir="ltr">+201001234567</a>`, `<a href="mailto:hi@mona.test">hi@mona.test</a>`,
				"<li>Visa · Mastercard · ميزة</li>",
				// the default home's WhatsApp section, now that there is a number
				`<section class="wa" id="s-help">`, "محتاج مساعدة في الطلب؟", "كلّمنا على واتساب ونرد عليك بسرعة",
				`href="https://wa.me/201001234567" rel="noopener" target="_blank">تواصل</a>`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, tt.opts...)
			if tt.contact {
				withContact(f)
			}
			rec := f.get(t, "/")
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d", rec.Code)
			}
			body := rec.Body.String()
			wantContains(t, body, tt.wants...)
			for _, n := range tt.notWanted {
				if strings.Contains(body, n) {
					t.Errorf("page has %q", n)
				}
			}
			checkCSP(t, rec)
		})
	}
}

func TestCategoryPageMarksItsLink(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	body := f.get(t, "/c/tshirts").Body.String()
	wantContains(t, body, `<a href="/c/tshirts" aria-current="page">تيشيرتات</a>`, `<a href="/">الرئيسية</a>`,
		`<span aria-current="page">تيشيرتات</span>`)
}

func TestCardBrandsByCountry(t *testing.T) {
	t.Parallel()
	tests := []struct {
		currency, ar, en string
	}{
		{"EGP", "Visa · Mastercard · ميزة", "Visa · Mastercard · Meeza"},
		{"SAR", "Visa · Mastercard · مدى", "Visa · Mastercard · mada"},
		{"AED", "Visa · Mastercard", "Visa · Mastercard"},
	}
	for _, tt := range tests {
		t.Run(tt.currency, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, withCards(fakePayments{on: true}), func(d *web.Deps) {
				d.Stores = fakeStores{tenancy.Store{Name: "متجر", Locale: "ar", Currency: money.Currency(tt.currency)}}
			})
			wantContains(t, f.get(t, "/").Body.String(), "<li>"+tt.ar+"</li>")
			wantContains(t, f.get(t, "/en/").Body.String(), "<li>"+tt.en+"</li>")
		})
	}
}

func TestDefaultHomeSections(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	body := f.get(t, "/").Body.String()
	wantContains(t, body,
		`<div class="secs">`,
		`<section class="sec hero" id="s-hero" data-align="start">`, `<a class="btn" href="#s-newest">تسوّق الآن</a>`,
		`<section class="sec tiles" id="s-cats">`, "<h2>تسوّق حسب التصنيف</h2>",
		`<a class="all" href="/" aria-current="page">الكل</a>`, `<a href="/c/tshirts"><i aria-hidden="true">ت</i>تيشيرتات</a>`,
		`<section class="sec badges" id="s-trust">`,
		`<section class="sec" id="s-newest">`, "<h2>وصل حديثًا</h2>", `<div class="grid">`,
	)
	if strings.Contains(body, `class="wa"`) {
		t.Fatal("WhatsApp section without a number")
	}
	// trust strip before the grid, as in the design
	if strings.Index(body, `id="s-trust"`) > strings.Index(body, `id="s-newest"`) {
		t.Fatal("sections out of order")
	}
}

func TestProductCards(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	one := product("شنطة جلد", "bag", 62000, true)
	was := egp(78000)
	one.Variants[0].CompareAt = &was
	f.catalog.products = append([]catalog.StorefrontProduct{one}, f.catalog.products...)
	body := f.get(t, "/").Body.String()
	wantContains(t, body,
		// one variant: adds straight to the cart
		`<form class="add" method="post" action="/cart"><input type="hidden" name="variant" value="`+one.Variants[0].ID.String()+`">`,
		`<input type="hidden" name="qty" value="1">`, `<button type="submit">أضف إلى السلة</button></form>`,
		`<span class="tag">خصم 20%</span>`, // 780 → 620 is 20.5%: rounded down
		`<span class="price sale" data-price>620 ج.م <s>780 ج.م</s></span>`,
		// two sizes: the button goes to the product, to choose one
		`<a class="add" href="/p/cotton-tee">أضف إلى السلة</a>`,
		// sold out: a gray tag, a plain price and no button
		`<span class="tag out">نفد</span>`, `<span class="price" data-price>99 ج.م </span>`,
	)
	if n := strings.Count(body, `class="add"`); n != 2 {
		t.Fatalf("%d add-to-cart controls, want 2 (none for the sold-out cap)", n)
	}
}

func TestNewSectionsRender(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	withContact(f)
	other := gen.New().String()
	f.content.landings["deals"] = domain.Page{ID: gen.New(), Kind: domain.KindLanding, Slug: "deals", Title: "العروض", Status: domain.StatusPublished,
		Sections: []domain.Section{
			{ID: "top", Type: domain.SectionHero, Settings: json.RawMessage(`{"badge":"تشكيلة جديدة","heading":"تشكيلة الخريف","button_label":"شوف","button_link":"/c/tshirts"}`)},
			{ID: "promos", Type: domain.SectionPromoTiles, Settings: json.RawMessage(`{"items":[{"label":"لفترة محدودة","title":"خصم 20%","link":"/c/tshirts"},{"title":"جديد","link":"https://example.com/x","tone":"green"}]}`)},
			{ID: "picked", Type: domain.SectionCategoryTiles, Settings: json.RawMessage(`{"title":"أقسامنا","category_ids":["` + other + `","` + f.cat.ID.String() + `"]}`)},
			{ID: "none", Type: domain.SectionCategoryTiles, Settings: json.RawMessage(`{"category_ids":["` + other + `"]}`)},
			{ID: "row", Type: domain.SectionProductGrid, Settings: json.RawMessage(`{"source":"category","category_id":"` + f.cat.ID.String() + `","layout":"carousel"}`)},
			{ID: "chat", Type: domain.SectionWhatsAppCTA, Settings: json.RawMessage(`{"title":"اسألنا","text":"نرد خلال دقائق"}`)},
		}}
	rec := f.get(t, "/pages/deals")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	wantContains(t, body,
		`<span class="pill">تشكيلة جديدة</span>`, "<h1>تشكيلة الخريف</h1>", `<a class="btn" href="/c/tshirts">شوف</a>`,
		`<section class="promos" id="s-promos">`,
		`<a class="promo" data-tone="amber" href="/c/tshirts"><span>لفترة محدودة</span> <strong>خصم 20%</strong></a>`,
		`<a class="promo" data-tone="green" href="https://example.com/x"><strong>جديد</strong></a>`,
		"<h2>أقسامنا</h2>", `<a class="all" href="/">الكل</a>`, // not the home page: "All" is not current
		`<div class="grid row">`, `<a href="/c/tshirts">عرض الكل</a>`,
		"<strong>اسألنا</strong>", "<p>نرد خلال دقائق</p>",
	)
	if strings.Contains(body, `id="s-none"`) {
		t.Fatal("category tiles whose categories are all gone must not render")
	}
	checkCSP(t, rec)
}

// The look chosen in the setup wizard (ADR-0034) marks the page, for the
// stylesheet; a store without one keeps the original layout.
func TestLookMarksThePage(t *testing.T) {
	t.Parallel()
	// The inlined stylesheet names the looks too: read the body tag only.
	bodyTag := func(f fixture) string {
		page := f.get(t, "/").Body.String()
		start := strings.Index(page, "<body")
		end := strings.Index(page[start:], ">")
		return page[start : start+end+1]
	}
	if tag := bodyTag(newFixture(t)); strings.Contains(tag, "data-look") {
		t.Fatalf("a store without a look is marked: %s", tag)
	}
	f := newFixture(t)
	f.content.mu.Lock()
	f.content.shop.Theme.Look = domain.LookSoft
	f.content.mu.Unlock()
	if tag := bodyTag(f); !strings.Contains(tag, `data-look="soft"`) {
		t.Fatalf("the soft look is not on the page: %s", tag)
	}
}
