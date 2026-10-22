package web_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/web"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
)

// countLimiter allows each key in allow that many times, then refuses;
// other keys are unlimited. fail makes every call an error (Redis down).
type countLimiter struct {
	mu    sync.Mutex
	used  map[string]int
	allow map[string]int
	fail  bool
}

func (c *countLimiter) Allow(_ context.Context, key string, _ ratelimit.Limit) (ratelimit.Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail {
		return ratelimit.Result{}, errors.New("redis down")
	}
	c.used[key]++
	if n, limited := c.allow[key]; limited && c.used[key] > n {
		return ratelimit.Result{}, nil
	}
	return ratelimit.Result{Allowed: true}, nil
}

// fakeCaptcha passes the token "ok".
type fakeCaptcha struct{ err error }

func (f fakeCaptcha) Verify(_ context.Context, token, _ string) (bool, error) {
	return token == "ok", f.err
}

type checkoutFixture struct {
	fixture
	orders  fakeOrders
	limiter *countLimiter
}

func newCheckoutFixture(t *testing.T, opts ...func(*web.Deps)) checkoutFixture {
	t.Helper()
	ord := fakeOrders{log: &orderLog{}}
	lim := &countLimiter{used: map[string]int{}, allow: map[string]int{}}
	f := newFixture(t, append([]func(*web.Deps){func(d *web.Deps) {
		d.Orders, d.Limiter, d.Captcha, d.CaptchaSiteKey = ord, lim, fakeCaptcha{}, "site-key"
		d.ClientIP = func(*http.Request) string { return "203.0.113.7" }
	}}, opts...)...)
	pid := f.shirt.ID.String()
	f.content.landings["offer"] = domain.Page{ID: gen.New(), Kind: domain.KindLanding, Slug: "offer", Title: "عرض", Status: domain.StatusPublished,
		Sections: []domain.Section{{ID: "main", Type: domain.SectionProductLanding,
			Settings: json.RawMessage(`{"product_id":"` + pid + `","cta_label":"اطلبيه الآن"}`)}}}
	return checkoutFixture{fixture: f, orders: ord, limiter: lim}
}

func (f checkoutFixture) orderForm() url.Values {
	return url.Values{"variant": {f.shirt.Variants[0].ID.String()}, "qty": {"2"}, "key": {"form-key-1"},
		"name": {"منى أحمد"}, "phone": {"010 1234 5678"}, "governorate": {"EG-ALX"}, "city": {"سموحة"},
		"address": {"14 شارع فوزي معاذ"}, "notes": {""}, "website": {""}}
}

// TestLandingQuickOrderForm: a landing page carries the one-step form,
// the same for every shopper (cacheable), with the store's governorates,
// a hidden honeypot and an empty key the script fills.
func TestLandingQuickOrderForm(t *testing.T) {
	t.Parallel()
	f := newCheckoutFixture(t)
	rec := f.get(t, "/pages/offer")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Cache-Control"), "public") {
		t.Fatalf("landing: %d %v", rec.Code, rec.Header())
	}
	wantContains(t, body, `action="/order"`, `data-order`, `name="key" value="" data-order-key`, `class="hp" aria-hidden="true"`,
		`name="website"`, `tabindex="-1"`, `<option value="EG-C">القاهرة</option>`, `<option value="EG-ALX">الإسكندرية</option>`,
		`name="phone"`, `type="tel"`, `name="address"`, ">اطلبيه الآن</button>", "الدفع نقدًا عند الاستلام")
	if strings.Contains(body, "challenges.cloudflare.com") || strings.Contains(rec.Header().Get("Content-Security-Policy"), "cloudflare") {
		t.Fatal("a cached page must not load the challenge")
	}
	if en := f.get(t, "/en/pages/offer").Body.String(); !strings.Contains(en, `<option value="EG-C">Cairo</option>`) || !strings.Contains(en, `action="/en/order"`) {
		t.Fatal("English form")
	}
}

func TestQuickOrder(t *testing.T) {
	t.Parallel()
	f := newCheckoutFixture(t)
	rec := f.post(t, "/order", f.orderForm(), nil, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/thank-you/IFT-1001" || rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("order: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	reqs := f.orders.placed()
	if len(reqs) != 1 {
		t.Fatalf("placed %d", len(reqs))
	}
	r := reqs[0]
	if r.Key != "form-key-1" || r.Name != "منى أحمد" || r.Phone != "010 1234 5678" || r.Governorate != "EG-ALX" ||
		len(r.Lines) != 1 || r.Lines[0].VariantID != f.shirt.Variants[0].ID || r.Lines[0].Qty != 2 {
		t.Fatalf("request %+v", r)
	}
	if rec = f.post(t, "/en/order", f.orderForm(), nil, nil); rec.Header().Get("Location") != "/en/thank-you/IFT-1001" {
		t.Fatalf("English redirect %q", rec.Header().Get("Location"))
	}
	if body := f.get(t, "/thank-you/IFT-1001").Body.String(); !strings.Contains(body, "IFT-1001") {
		t.Fatal("thank-you page")
	}
}

func TestQuickOrderProblems(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		edit    func(url.Values)
		err     error
		headers map[string]string
		status  int
		want    []string
		placed  bool
	}{
		{name: "invalid fields keep the values", err: &web.OrderInvalidError{Fields: []string{"phone", "address"}},
			status: http.StatusUnprocessableEntity, placed: true,
			want: []string{"راجع البيانات المظللة.", "اكتب رقم موبايل صحيح.", "اكتب العنوان بالتفصيل", `value="منى أحمد"`,
				`<option value="EG-ALX" selected>`, `name="variant" value=`, "300 ج.م"}},
		{name: "blocked reads as a refusal", err: web.ErrOrderRefused, status: http.StatusUnprocessableEntity, placed: true,
			want: []string{"تعذّر إتمام الطلب. تواصل مع المتجر."}},
		{name: "out of stock", err: web.ErrOrderOutOfStock, status: http.StatusConflict, placed: true, want: []string{"الكمية المطلوبة غير متوفرة."}},
		{name: "item gone", err: &web.OrderInvalidError{Fields: []string{"lines"}}, status: http.StatusUnprocessableEntity, placed: true,
			want: []string{"هذا المنتج لم يعد متاحًا."}},
		{name: "honeypot", edit: func(v url.Values) { v.Set("website", "http://spam.example") }, status: http.StatusUnprocessableEntity,
			want: []string{"تعذّر إتمام الطلب. تواصل مع المتجر."}},
		{name: "bad quantity", edit: func(v url.Values) { v.Set("qty", "99") }, status: http.StatusUnprocessableEntity, want: []string{"اختر كمية من 1 إلى 20."}},
		{name: "bad variant", edit: func(v url.Values) { v.Set("variant", "x") }, status: http.StatusUnprocessableEntity},
		{name: "cross-site", headers: map[string]string{"Origin": "https://evil.example"}, status: http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newCheckoutFixture(t)
			f.orders.log.err = tt.err
			form := f.orderForm()
			if tt.edit != nil {
				tt.edit(form)
			}
			rec := f.post(t, "/order", form, nil, tt.headers)
			if rec.Code != tt.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, tt.status, rec.Body)
			}
			wantContains(t, rec.Body.String(), tt.want...)
			if tt.status != http.StatusForbidden && (rec.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(rec.Body.String(), "noindex")) {
				t.Fatalf("a form page must be private: %v", rec.Header())
			}
			if got := len(f.orders.placed()) == 1; got != tt.placed {
				t.Fatalf("order placed = %v, want %v", got, tt.placed)
			}
		})
	}
}

// TestAdaptiveChallenge: under the soft limits orders go straight
// through; past them the shopper must pass Turnstile (and only that
// private response allows its script); past the hard limits, or the
// store's, the order is refused; a limiter failure asks for a challenge.
func TestAdaptiveChallenge(t *testing.T) {
	t.Parallel()
	f := newCheckoutFixture(t)
	f.limiter.allow = map[string]int{"order:ip:203.0.113.7:soft": 1, "order:ip:203.0.113.7:hard": 3}

	if rec := f.post(t, "/order", f.orderForm(), nil, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("first order: %d", rec.Code)
	}
	rec := f.post(t, "/order", f.orderForm(), nil, nil)
	csp := rec.Header().Get("Content-Security-Policy")
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(csp, "script-src 'self' https://challenges.cloudflare.com") ||
		!strings.Contains(csp, "frame-src https://challenges.cloudflare.com") {
		t.Fatalf("second order: %d %q", rec.Code, csp)
	}
	wantContains(t, rec.Body.String(), `class="cf-turnstile" data-sitekey="site-key"`, "challenges.cloudflare.com/turnstile/v0/api.js",
		"أكّد أنك لست روبوتًا", `value="منى أحمد"`)
	if len(f.orders.placed()) != 1 {
		t.Fatal("a challenged order was placed")
	}
	form := f.orderForm()
	form.Set("cf-turnstile-response", "wrong")
	if rec = f.post(t, "/order", form, nil, nil); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "لم نتمكن من التحقق") {
		t.Fatalf("wrong answer: %d", rec.Code)
	}
	form.Set("cf-turnstile-response", "ok")
	if rec = f.post(t, "/order", form, nil, nil); rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("past the hard limit: %d", rec.Code) // 4th submission from this IP
	}

	g := newCheckoutFixture(t)
	g.limiter.allow = map[string]int{"order:ip:203.0.113.7:soft": 0}
	form = g.orderForm()
	form.Set("cf-turnstile-response", "ok")
	if rec = g.post(t, "/order", form, nil, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("a passed challenge must place the order: %d %s", rec.Code, rec.Body)
	}

	down := newCheckoutFixture(t)
	down.limiter.fail = true
	if rec = down.post(t, "/order", down.orderForm(), nil, nil); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "cf-turnstile") {
		t.Fatalf("limiter down: %d", rec.Code)
	}

	noCaptcha := newCheckoutFixture(t, func(d *web.Deps) { d.Captcha, d.CaptchaSiteKey = nil, "" })
	noCaptcha.limiter.allow = map[string]int{"order:ip:203.0.113.7:soft": 0}
	if rec = noCaptcha.post(t, "/order", noCaptcha.orderForm(), nil, nil); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("no way to verify: %d", rec.Code)
	}
	unreachable := newCheckoutFixture(t, func(d *web.Deps) { d.Captcha = fakeCaptcha{err: errors.New("cloudflare down")} })
	unreachable.limiter.allow = map[string]int{"order:ip:203.0.113.7:soft": 0}
	form = unreachable.orderForm()
	form.Set("cf-turnstile-response", "ok")
	if rec = unreachable.post(t, "/order", form, nil, nil); rec.Code != http.StatusUnprocessableEntity || len(unreachable.orders.placed()) != 0 {
		t.Fatalf("captcha unreachable: %d", rec.Code)
	}
}

// TestPhoneLimitKey: the per-phone limits count one number however it is
// typed (its last 9 digits).
func TestPhoneLimitKey(t *testing.T) {
	t.Parallel()
	f := newCheckoutFixture(t)
	for _, p := range []string{"01012345678", "+20 10 1234 5678", "٠١٠١٢٣٤٥٦٧٨"} {
		form := f.orderForm()
		form.Set("phone", p)
		f.post(t, "/order", form, nil, nil)
	}
	f.limiter.mu.Lock()
	defer f.limiter.mu.Unlock()
	key := "order:phone:" + f.store.String() + ":012345678:soft"
	if f.limiter.used[key] != 3 {
		t.Fatalf("%s used %d times, want 3 (keys %v)", key, f.limiter.used[key], f.limiter.used)
	}
}

func TestCheckoutFromCart(t *testing.T) {
	t.Parallel()
	f := newCheckoutFixture(t)
	m := f.shirt.Variants[0].ID.String()
	if rec := f.get(t, "/checkout"); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/cart" {
		t.Fatalf("checkout without a cart: %d", rec.Code)
	}
	add := f.post(t, "/cart", url.Values{"variant": {m}, "qty": {"2"}}, nil, nil)
	cookies := []*http.Cookie{cookie(add, "__Host-cart")}
	rec := f.getWith(t, "/checkout", cookies)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("checkout page: %d", rec.Code)
	}
	wantContains(t, rec.Body.String(), "ملخص الطلب", "تيشيرت قطن × 2", "300 ج.م", `action="/checkout"`, `name="governorate"`)
	form := f.orderForm()
	form.Del("variant")
	form.Del("qty")
	rec = f.post(t, "/checkout", form, cookies, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/thank-you/IFT-1001" || cookie(rec, "cart_n").Value != "0" {
		t.Fatalf("checkout: %d %v", rec.Code, rec.Header())
	}
	reqs := f.orders.placed()
	if len(reqs) != 1 || len(reqs[0].Lines) != 1 || reqs[0].Lines[0].Qty != 2 || reqs[0].Lines[0].VariantID.String() != m {
		t.Fatalf("checkout request %+v", reqs)
	}
	if body := f.getWith(t, "/cart", cookies).Body.String(); !strings.Contains(body, "سلتك فارغة.") {
		t.Fatal("the cart must be empty after the order")
	}
	// Without the orders module there is no checkout.
	if rec = newFixture(t).getWith(t, "/checkout", cookies); rec.Code != http.StatusNotFound {
		t.Fatalf("no orders: %d", rec.Code)
	}
}
