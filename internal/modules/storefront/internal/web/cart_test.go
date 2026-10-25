package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/web"
	"github.com/roshbaikfb/iftahonline/internal/platform/authtoken"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
)

const host = "mona.iftahonline.com"

// post submits a cart form as a browser on the store's own pages would.
func (f fixture) post(t *testing.T, path string, form url.Values, cookies []*http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	rq := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, strings.NewReader(form.Encode()))
	rq.Host = host
	rq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rq.Header.Set("Origin", "https://"+host)
	for k, v := range headers {
		if v == "" {
			rq.Header.Del(k)
		} else {
			rq.Header.Set(k, v)
		}
	}
	for _, c := range cookies {
		rq.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, rq)
	return rec
}

func (f fixture) getWith(t *testing.T, path string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	rq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	rq.Host = host
	for _, c := range cookies {
		rq.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, rq)
	return rec
}

func cookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestCartFlow(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	m := f.shirt.Variants[0].ID.String()

	if rec := f.get(t, "/cart"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "سلتك فارغة.") ||
		rec.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(rec.Body.String(), `<meta name="robots" content="noindex">`) {
		t.Fatalf("empty cart: %d %v", rec.Code, rec.Header())
	}
	rec := f.post(t, "/cart", url.Values{"variant": {m}, "qty": {"2"}}, nil, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/cart" || rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("add: %d %v", rec.Code, rec.Header())
	}
	tok, units := cookie(rec, "__Host-cart"), cookie(rec, "cart_n")
	if tok == nil || !tok.HttpOnly || !tok.Secure || tok.SameSite != http.SameSiteLaxMode || tok.Path != "/" || tok.Domain != "" {
		t.Fatalf("cart cookie %+v", tok)
	}
	if units == nil || units.Value != "2" || units.HttpOnly {
		t.Fatalf("units cookie %+v", units)
	}
	cookies := []*http.Cookie{tok}
	page := f.getWith(t, "/cart", cookies).Body.String()
	for _, want := range []string{"تيشيرت قطن", "<small>M</small>", `value="2"`, "300 ج.م", "المجموع", `action="/cart/update"`, `href="/checkout">إتمام الطلب`} {
		if !strings.Contains(page, want) {
			t.Errorf("cart page lacks %q", want)
		}
	}
	// English cart, same cookie; redirects stay in the language.
	if rec = f.post(t, "/en/cart/update", url.Values{"variant": {m}, "qty": {"5"}}, cookies, nil); rec.Header().Get("Location") != "/en/cart" || cookie(rec, "cart_n").Value != "5" {
		t.Fatalf("English update: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if !strings.Contains(f.getWith(t, "/en/cart", cookies).Body.String(), "EGP 750") {
		t.Fatal("English cart total")
	}
	if rec = f.post(t, "/cart/update", url.Values{"variant": {m}, "qty": {"0"}}, cookies, nil); cookie(rec, "cart_n").Value != "0" {
		t.Fatal("removing the line must zero the badge")
	}
	if !strings.Contains(f.getWith(t, "/cart", cookies).Body.String(), "سلتك فارغة.") {
		t.Fatal("line not removed")
	}
	// Pages stay the same for everyone: the cookie changes nothing there.
	if a, b := f.get(t, "/p/cotton-tee").Body.String(), f.getWith(t, "/p/cotton-tee", cookies).Body.String(); a != b {
		t.Fatal("a cached page varies with the cart cookie")
	}
}

func TestCartErrors(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	m := f.shirt.Variants[0].ID.String()
	tests := []struct {
		name    string
		form    url.Values
		headers map[string]string
		status  int
		message string
	}{
		{"unknown variant", url.Values{"variant": {gen.New().String()}, "qty": {"1"}}, nil, http.StatusConflict, "هذا المنتج لم يعد متاحًا."},
		{"malformed variant", url.Values{"variant": {"x"}, "qty": {"1"}}, nil, http.StatusUnprocessableEntity, "هذا المنتج لم يعد متاحًا."},
		{"bad quantity", url.Values{"variant": {m}, "qty": {"many"}}, nil, http.StatusUnprocessableEntity, "اختر كمية من 1 إلى 20."},
		{"too many", url.Values{"variant": {m}, "qty": {"50"}}, nil, http.StatusUnprocessableEntity, "اختر كمية من 1 إلى 20."},
		{"cross-site form", url.Values{"variant": {m}, "qty": {"1"}}, map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden, ""},
		{"no origin, cross-site fetch", url.Values{"variant": {m}, "qty": {"1"}}, map[string]string{"Origin": "", "Sec-Fetch-Site": "cross-site"}, http.StatusForbidden, ""},
		{"no origin, no hints", url.Values{"variant": {m}, "qty": {"1"}}, map[string]string{"Origin": ""}, http.StatusForbidden, ""},
		{"same-origin hint", url.Values{"variant": {m}, "qty": {"1"}}, map[string]string{"Origin": "", "Sec-Fetch-Site": "same-origin"}, http.StatusSeeOther, ""},
		{"same-origin referer", url.Values{"variant": {m}, "qty": {"1"}}, map[string]string{"Origin": "", "Referer": "https://" + host + "/p/cotton-tee"}, http.StatusSeeOther, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := f.post(t, "/cart", tt.form, nil, tt.headers)
			if rec.Code != tt.status || (tt.message != "" && !strings.Contains(rec.Body.String(), tt.message)) {
				t.Fatalf("status %d (want %d): %s", rec.Code, tt.status, rec.Body.String())
			}
			if tt.status >= 400 && cookie(rec, "__Host-cart") != nil {
				t.Fatal("a refused write set the cart cookie")
			}
		})
	}
	big := url.Values{"variant": {m}, "qty": {"1"}, "pad": {strings.Repeat("x", 8<<10)}}
	if rec := f.post(t, "/cart", big, nil, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized form: %d", rec.Code)
	}
}

// denyLimiter refuses everything, as an exhausted limit would.
type denyLimiter struct{ keys []string }

func (d *denyLimiter) Allow(_ context.Context, key string, _ ratelimit.Limit) (ratelimit.Result, error) {
	d.keys = append(d.keys, key)
	return ratelimit.Result{Allowed: false}, nil
}

func TestCartRateLimit(t *testing.T) {
	t.Parallel()
	lim := &denyLimiter{}
	f := newFixture(t, func(d *web.Deps) {
		d.Limiter = lim
		d.ClientIP = func(*http.Request) string { return "203.0.113.7" }
	})
	rec := f.post(t, "/cart", url.Values{"variant": {f.shirt.Variants[0].ID.String()}, "qty": {"1"}}, nil, nil)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" || !strings.Contains(rec.Body.String(), "طلبات كثيرة") {
		t.Fatalf("limited: %d", rec.Code)
	}
	if len(lim.keys) == 0 || lim.keys[0] != "cart:ip:203.0.113.7" {
		t.Fatalf("limit keys %q", lim.keys)
	}
}

func TestThankYou(t *testing.T) {
	t.Parallel()
	f := newFixture(t, func(d *web.Deps) { d.Orders = fakeOrders{} })
	rec := f.get(t, "/thank-you/IFT-1001")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "private, no-store" ||
		!strings.Contains(body, "شكرًا لطلبك!") || !strings.Contains(body, "IFT-1001") || !strings.Contains(body, "300 ج.م") ||
		!strings.Contains(body, `content="noindex"`) {
		t.Fatalf("thank you: %d %s", rec.Code, body)
	}
	if rec = f.get(t, "/thank-you/IFT-9999"); rec.Code != http.StatusNotFound || rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("unknown order: %d", rec.Code)
	}
	// Without the orders module the page does not exist.
	if rec = newFixture(t).get(t, "/thank-you/IFT-1001"); rec.Code != http.StatusNotFound {
		t.Fatalf("no orders: %d", rec.Code)
	}
}

// TestAddByChoice: without JavaScript the product page posts its
// choices (one per option); the server picks the variant they name.
func TestAddByChoice(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	pid := f.shirt.ID.String()
	add := func(form url.Values) *httptest.ResponseRecorder {
		form.Set("qty", "1")
		return f.post(t, "/cart", form, nil, nil)
	}
	rec := add(url.Values{"product": {pid}, "o0": {"M"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("add by choice = %d", rec.Code)
	}
	tok := ""
	for _, c := range rec.Result().Cookies() {
		if c.Name == "__Host-cart" {
			tok = c.Value
		}
	}
	if lines := f.carts.carts[authtoken.Token(tok)]; lines[f.shirt.Variants[0].ID] != 1 || len(lines) != 1 {
		t.Fatalf("cart %v, want one M", lines)
	}
	for name, form := range map[string]url.Values{
		"a size the product does not have": {"product": {pid}, "o0": {"XXL"}},
		"another store's product, or none": {"product": {gen.New().String()}, "o0": {"M"}},
		"no choice at all":                 {"product": {pid}},
	} {
		if rec := add(form); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "هذا المنتج لم يعد متاحًا.") {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if c := f.catalog.calls0("byIDs"); c != 4 {
		t.Fatalf("ProductsByIDs called %d times for 4 posts naming a product, want one each", c)
	}
}
