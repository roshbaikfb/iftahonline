package web_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/marketing"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/web"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
)

// fakePixels is a store's pixels; asked counts the reads.
type fakePixels struct {
	b     marketing.Browser
	err   error
	asked *atomic.Int64
}

func (p fakePixels) Browser(context.Context) (marketing.Browser, error) {
	if p.asked != nil {
		p.asked.Add(1)
	}
	return p.b, p.err
}

func metaAndGA4(consent marketing.Consent) fakePixels {
	return fakePixels{b: marketing.Browser{
		Pixels:  []marketing.BrowserPixel{{Provider: marketing.Meta, PixelID: "123456789012345"}, {Provider: marketing.GA4, PixelID: "G-ABC123XYZ"}},
		Consent: consent,
		Script:  []string{"https://connect.facebook.net", "https://www.googletagmanager.com"},
		Connect: []string{"https://www.facebook.com"},
		Frame:   []string{"https://td.doubleclick.net"},
	}}
}

func withPixels(p fakePixels) func(*web.Deps) { return func(d *web.Deps) { d.Pixels = p } }

type pxConfig struct {
	Pixels []struct{ K, ID, L string } `json:"p"`
	Ask    bool                        `json:"ask"`
	Events []struct {
		N   string   `json:"n"`
		ID  string   `json:"id"`
		V   int64    `json:"v"`
		U   int      `json:"u"`
		C   string   `json:"c"`
		IDs []string `json:"ids"`
		Ref string   `json:"ref"`
	} `json:"ev"`
}

var pxRE = regexp.MustCompile(`<script type="application/json" id="px">(.*?)</script>`)

// px reads a page's pixel configuration; ok is false without one.
func px(t *testing.T, body string) (pxConfig, bool) {
	t.Helper()
	m := pxRE.FindStringSubmatch(body)
	if m == nil {
		return pxConfig{}, false
	}
	var c pxConfig
	if err := json.Unmarshal([]byte(m[1]), &c); err != nil {
		t.Fatalf("px config %q: %v", m[1], err)
	}
	return c, true
}

// TestNoPixels: a store without pixels loads nothing more, and its CSP
// opens nothing.
func TestNoPixels(t *testing.T) {
	t.Parallel()
	for name, opt := range map[string]func(*web.Deps){"none connected": withPixels(fakePixels{}), "no module": func(*web.Deps) {}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, opt)
			rec := f.get(t, "/p/cotton-tee")
			body := rec.Body.String()
			if _, ok := px(t, body); ok || strings.Contains(body, "/assets/px.") || strings.Contains(body, "data-consent") {
				t.Fatal("a store without pixels loads them")
			}
			if c := rec.Header().Get("Content-Security-Policy"); strings.Contains(c, "facebook") || !strings.Contains(c, "script-src 'self';") {
				t.Fatalf("CSP %q", c)
			}
		})
	}
}

// TestPixelsOnPages: a store's pixels are on every page with px.js; the
// CSP opens exactly their origins; the product page reports ViewContent.
func TestPixelsOnPages(t *testing.T) {
	t.Parallel()
	f := newFixture(t, withPixels(metaAndGA4(marketing.ConsentAuto)))
	rec := f.get(t, "/p/cotton-tee")
	body := rec.Body.String()
	c, ok := px(t, body)
	if !ok || len(c.Pixels) != 2 || c.Pixels[0].K != "meta" || c.Pixels[0].ID != "123456789012345" || c.Pixels[1].K != "ga4" || c.Ask {
		t.Fatalf("px config %+v", c)
	}
	wantContains(t, body, `<script type="module" src="/assets/px.`)
	if strings.Contains(body, "data-consent") {
		t.Fatal("an Egyptian store on auto asks for consent")
	}
	if len(c.Events) != 1 || c.Events[0].N != web.EventViewContent || c.Events[0].V != 15000 || c.Events[0].U != 2 || c.Events[0].C != "EGP" ||
		c.Events[0].IDs[0] != shirtM.String() || c.Events[0].ID != "" {
		t.Fatalf("product page events %+v", c.Events)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"script-src 'self' https://connect.facebook.net https://www.googletagmanager.com;", "connect-src 'self' https://www.facebook.com;",
		"frame-src https://td.doubleclick.net"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP lacks %q: %s", want, csp)
		}
	}
	checkCSP(t, rec)
	if home, _ := px(t, f.get(t, "/").Body.String()); len(home.Pixels) != 2 || len(home.Events) != 0 {
		t.Fatalf("home %+v", home)
	}
}

// TestConsent: the banner shows where the store asks first: always when
// required, on auto only in Saudi Arabia; never when off.
func TestConsent(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		consent  marketing.Consent
		currency string
		ask      bool
	}{
		{marketing.ConsentAuto, "EGP", false},
		{marketing.ConsentAuto, "SAR", true},
		{marketing.ConsentRequired, "EGP", true},
		{marketing.ConsentOff, "SAR", false},
	} {
		t.Run(string(tc.consent)+" "+tc.currency, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, withPixels(metaAndGA4(tc.consent)), func(d *web.Deps) {
				d.Stores = fakeStores{tenancy.Store{Name: "متجر", Locale: "ar", Currency: "EGP"}}
				if tc.currency == "SAR" {
					d.Stores = fakeStores{tenancy.Store{Name: "متجر", Locale: "ar", Currency: "SAR"}}
				}
			})
			body := f.get(t, "/").Body.String()
			c, _ := px(t, body)
			banner := strings.Contains(body, `<div class="consent" data-consent hidden`)
			if c.Ask != tc.ask || banner != tc.ask {
				t.Fatalf("ask %v, banner %v; want %v", c.Ask, banner, tc.ask)
			}
			if tc.ask {
				wantContains(t, body, "data-consent-yes>موافق</button>", "data-consent-no>لا، شكرًا</button>")
			}
		})
	}
}

// TestPixelsFailOpen: when the pixels cannot be read, the store's pages
// render without them, and the read is cached with the view.
func TestPixelsFailOpen(t *testing.T) {
	t.Parallel()
	asked := &atomic.Int64{}
	f := newFixture(t, withPixels(fakePixels{err: errors.New("db down"), asked: asked}))
	for range 3 {
		rec := f.get(t, "/")
		if rec.Code != http.StatusOK {
			t.Fatalf("home = %d", rec.Code)
		}
		if _, ok := px(t, rec.Body.String()); ok {
			t.Fatal("pixels on a page whose pixels could not be read")
		}
	}
	if n := asked.Load(); n != 1 {
		t.Fatalf("asked %d times, want once per view", n)
	}
	f.shop.Invalidate(f.store)
	f.get(t, "/")
	if n := asked.Load(); n != 2 {
		t.Fatalf("after an invalidation asked %d times, want 2", n)
	}
}

// TestCheckoutConversions: adding to the cart reports AddToCart once on
// the cart page, with an event_id the server made and kept; checkout
// reports InitiateCheckout with one event_id per cart and lines; the
// thank-you page reports Purchase with the order's event_id.
func TestCheckoutConversions(t *testing.T) {
	t.Parallel()
	f := newCheckoutFixture(t, withPixels(metaAndGA4(marketing.ConsentOff)))
	variant := f.shirt.Variants[0].ID.String()
	add := f.post(t, "/cart", url.Values{"variant": {variant}, "qty": {"2"}}, nil, nil)
	if cookie(add, "__Host-px_add") != nil {
		t.Fatal("the add's event_id left the server")
	}
	cart := cookie(add, "__Host-cart")
	c, _ := px(t, f.getWith(t, "/cart", []*http.Cookie{cart}).Body.String())
	if len(c.Events) != 1 || c.Events[0].N != web.EventAddToCart || len(c.Events[0].ID) != 26 || c.Events[0].V != 30000 || c.Events[0].IDs[0] != variant {
		t.Fatalf("cart events %+v", c.Events)
	}
	if again, _ := px(t, f.getWith(t, "/cart", []*http.Cookie{cart}).Body.String()); len(again.Events) != 0 {
		t.Fatalf("the cart reports the add twice: %+v", again.Events)
	}

	co, _ := px(t, f.getWith(t, "/checkout", []*http.Cookie{cart}).Body.String())
	if len(co.Events) != 1 || co.Events[0].N != web.EventInitiateCheckout || !strings.HasPrefix(co.Events[0].ID, "ic-") || len(co.Events[0].ID) != 35 ||
		co.Events[0].V != 30000 {
		t.Fatalf("checkout events %+v", co.Events)
	}
	// A reload is the same checkout; another cart is another.
	if re, _ := px(t, f.getWith(t, "/checkout", []*http.Cookie{cart}).Body.String()); re.Events[0].ID != co.Events[0].ID {
		t.Fatalf("a reload made a new event_id: %s, %s", co.Events[0].ID, re.Events[0].ID)
	}
	// An update is not an add, and changes the checkout.
	f.post(t, "/cart/update", url.Values{"variant": {variant}, "qty": {"1"}}, []*http.Cookie{cart}, nil)
	if after, _ := px(t, f.getWith(t, "/cart", []*http.Cookie{cart}).Body.String()); len(after.Events) != 0 {
		t.Fatalf("a quantity change reported as an add: %+v", after.Events)
	}
	if changed, _ := px(t, f.getWith(t, "/checkout", []*http.Cookie{cart}).Body.String()); changed.Events[0].ID == co.Events[0].ID {
		t.Fatal("another cart kept the checkout's event_id")
	}

	ty, _ := px(t, f.get(t, "/thank-you/IFT-1001").Body.String())
	if len(ty.Events) != 1 || ty.Events[0].N != web.EventPurchase || ty.Events[0].ID != "purchase:IFT-1001" || ty.Events[0].Ref != "IFT-1001" || ty.Events[0].V == 0 {
		t.Fatalf("thank-you events %+v", ty.Events)
	}
}

// TestAddedNotFromCookie: the shopper cannot name an AddToCart: a cookie
// of the old form, or any other, reports nothing.
func TestAddedNotFromCookie(t *testing.T) {
	t.Parallel()
	conv := &tracked{}
	f := newCheckoutFixture(t, withPixels(metaAndGA4(marketing.ConsentOff)), func(d *web.Deps) { d.Conversions = conv })
	variant := f.shirt.Variants[0].ID.String()
	add := f.post(t, "/cart", url.Values{"variant": {variant}, "qty": {"1"}}, nil, nil)
	cart := cookie(add, "__Host-cart")
	f.getWith(t, "/cart", []*http.Cookie{cart}) // the real add, reported
	for _, v := range []string{"01M5PAGE000000000000000001." + variant + ".1", "01M5PAGE000000000000000002." + variant + ".5", "x"} {
		rec := f.getWith(t, "/cart", []*http.Cookie{cart, {Name: "__Host-px_add", Value: v}})
		if c, _ := px(t, rec.Body.String()); len(c.Events) != 0 {
			t.Errorf("cookie %q reported %+v", v, c.Events)
		}
	}
	if got := conv.all(); len(got) != 1 || got[0].Name != marketing.AddToCart {
		t.Fatalf("server events %+v", got)
	}
}

// TestNoAddedWithoutPixels: a store without pixels keeps no add.
func TestNoAddedWithoutPixels(t *testing.T) {
	t.Parallel()
	added := &fakeAdded{kept: map[string]string{}}
	f := newCheckoutFixture(t, func(d *web.Deps) { d.Added = added })
	f.post(t, "/cart", url.Values{"variant": {f.shirt.Variants[0].ID.String()}, "qty": {"1"}}, nil, nil)
	if added.len() != 0 {
		t.Fatal("a store without pixels kept an add")
	}
}

// TestServerConversionsLimited: past the limits, the server reports no
// more conversions; the page still does, from the browser.
func TestServerConversionsLimited(t *testing.T) {
	t.Parallel()
	conv := &tracked{}
	f := newCheckoutFixture(t, withPixels(metaAndGA4(marketing.ConsentOff)), func(d *web.Deps) { d.Conversions = conv })
	f.limiter.mu.Lock()
	f.limiter.allow["conversion:ip:203.0.113.7"] = 2
	f.limiter.mu.Unlock()
	cookies, _ := checkoutCart(t, f)
	for range 5 {
		if c, _ := px(t, f.getWith(t, "/checkout", cookies).Body.String()); len(c.Events) != 1 {
			t.Fatalf("checkout events %+v", c.Events)
		}
	}
	if got := conv.all(); len(got) != 2 {
		t.Fatalf("%d server events past a limit of 2", len(got))
	}
	// The limiter down: no server events (they cost a call each), pages fine.
	quiet := &tracked{}
	g := newCheckoutFixture(t, withPixels(metaAndGA4(marketing.ConsentOff)), func(d *web.Deps) { d.Conversions = quiet })
	gCookies, _ := checkoutCart(t, g)
	g.limiter.mu.Lock()
	g.limiter.fail = true
	g.limiter.mu.Unlock()
	rec := g.getWith(t, "/checkout", gCookies)
	if c, _ := px(t, rec.Body.String()); rec.Code != http.StatusOK || len(c.Events) != 1 {
		t.Fatalf("checkout without a limiter: %d %+v", rec.Code, c.Events)
	}
	if len(quiet.all()) != 0 {
		t.Fatal("a server event without a working limiter")
	}
}

// TestOrderVisit (ADR-0043): an order carries the campaign app.js
// remembered; the ad signals only where the store's pixels may run for
// the shopper.
func TestOrderVisit(t *testing.T) {
	t.Parallel()
	src := &http.Cookie{Name: "ift_src", Value: url.QueryEscape("utm_source=facebook&utm_campaign=eid&fbclid=IwAR0&ts=1730000000000&gclid=Cj0&ttclid=E.C.P")}
	signals := []*http.Cookie{{Name: "_fbp", Value: "fb.1.1730000000000.42"}, {Name: "_ttp", Value: "ttp-1"}}
	place := func(t *testing.T, f checkoutFixture, extra ...*http.Cookie) web.Visit {
		t.Helper()
		cookies, form := checkoutCart(t, f)
		rec := f.post(t, "/checkout", form, append(cookies, extra...), map[string]string{"User-Agent": "Mozilla/5.0 test"})
		reqs := f.orders.placed()
		if rec.Code != http.StatusSeeOther || len(reqs) == 0 {
			t.Fatalf("checkout = %d %s", rec.Code, rec.Body.String())
		}
		return reqs[len(reqs)-1].Visit
	}

	t.Run("no pixels: the campaign only", func(t *testing.T) {
		t.Parallel()
		v := place(t, newCheckoutFixture(t), append([]*http.Cookie{src}, signals...)...)
		if v.UTMSource != "facebook" || v.UTMCampaign != "eid" || v.FBP != "" || v.GCLID != "" || v.IP != "" || v.UserAgent != "" {
			t.Fatalf("visit %+v", v)
		}
	})
	t.Run("pixels, no consent asked", func(t *testing.T) {
		t.Parallel()
		v := place(t, newCheckoutFixture(t, withPixels(metaAndGA4(marketing.ConsentOff))), append([]*http.Cookie{src}, signals...)...)
		if v.UTMSource != "facebook" || v.FBP != "fb.1.1730000000000.42" || v.FBC != "fb.1.1730000000000.IwAR0" || v.TTP != "ttp-1" ||
			v.GCLID != "Cj0" || v.TTCLID != "E.C.P" || v.IP == "" || v.UserAgent != "Mozilla/5.0 test" {
			t.Fatalf("visit %+v", v)
		}
	})
	t.Run("Meta's own fbc wins", func(t *testing.T) {
		t.Parallel()
		v := place(t, newCheckoutFixture(t, withPixels(metaAndGA4(marketing.ConsentOff))), src, &http.Cookie{Name: "_fbc", Value: "fb.1.1.own"})
		if v.FBC != "fb.1.1.own" {
			t.Fatalf("fbc %q", v.FBC)
		}
	})
	for name, answer := range map[string]string{"consent not given": "", "consent declined": "no"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cookies := append([]*http.Cookie{src}, signals...)
			if answer != "" {
				cookies = append(cookies, &http.Cookie{Name: "px_c", Value: answer})
			}
			v := place(t, newCheckoutFixture(t, withPixels(metaAndGA4(marketing.ConsentRequired))), cookies...)
			if v.UTMSource != "facebook" || v.FBP != "" || v.GCLID != "" || v.IP != "" || v.UserAgent != "" {
				t.Fatalf("visit %+v", v)
			}
		})
	}
	t.Run("consent given", func(t *testing.T) {
		t.Parallel()
		cookies := append([]*http.Cookie{src, {Name: "px_c", Value: "yes"}}, signals...)
		if v := place(t, newCheckoutFixture(t, withPixels(metaAndGA4(marketing.ConsentRequired))), cookies...); v.FBP == "" || v.IP == "" {
			t.Fatalf("visit %+v", v)
		}
	})
	t.Run("a cookie that is not a query", func(t *testing.T) {
		t.Parallel()
		v := place(t, newCheckoutFixture(t), &http.Cookie{Name: "ift_src", Value: "%zz"})
		if v != (web.Visit{}) {
			t.Fatalf("visit %+v", v)
		}
	})
}

// tracked keeps the conversions the server reported, and fails with err.
type tracked struct {
	mu  sync.Mutex
	got []marketing.Conversion
	err error
}

func (c *tracked) Track(_ context.Context, cv marketing.Conversion) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got = append(c.got, cv)
	return c.err
}

func (c *tracked) all() []marketing.Conversion {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]marketing.Conversion(nil), c.got...)
}

// TestServerConversions (ADR-0043): the cart's and checkout's
// conversions go to the server with the event_id the page reports, with
// the visit's signals; never without consent where the store asks; a
// failure costs the event, not the page.
func TestServerConversions(t *testing.T) {
	t.Parallel()
	conv := &tracked{}
	f := newCheckoutFixture(t, withPixels(metaAndGA4(marketing.ConsentOff)), func(d *web.Deps) { d.Conversions = conv })
	variant := f.shirt.Variants[0].ID.String()
	add := f.post(t, "/cart", url.Values{"variant": {variant}, "qty": {"2"}}, nil, nil)
	cart := cookie(add, "__Host-cart")
	page, _ := px(t, f.getWith(t, "/cart", []*http.Cookie{cart, {Name: "_fbp", Value: "fb.1.2.3"}}).Body.String())
	checkout, _ := px(t, f.getWith(t, "/checkout", []*http.Cookie{cart}).Body.String())
	got := conv.all()
	if len(got) != 2 || len(page.Events) != 1 || len(checkout.Events) != 1 {
		t.Fatalf("server %+v, pages %+v %+v", got, page.Events, checkout.Events)
	}
	if got[0].Name != marketing.AddToCart || got[0].EventID != page.Events[0].ID || got[0].Value.Amount() != 30000 || got[0].Items[0] != variant ||
		got[0].User.FBP != "fb.1.2.3" || got[0].User.IP == "" || got[0].URL != "https://"+host+"/cart" {
		t.Fatalf("AddToCart %+v (page %+v)", got[0], page.Events[0])
	}
	if got[1].Name != marketing.InitiateCheckout || got[1].EventID != checkout.Events[0].ID || got[1].Value.Amount() != 30000 {
		t.Fatalf("InitiateCheckout %+v (page %+v)", got[1], checkout.Events[0])
	}

	// A failure: the page renders all the same.
	conv.err = errors.New("db down")
	if rec := f.getWith(t, "/checkout", []*http.Cookie{cart}); rec.Code != http.StatusOK {
		t.Fatalf("checkout with a failing report = %d", rec.Code)
	}

	// Consent asked and not given: the page reports, the server does not.
	quiet := &tracked{}
	g := newCheckoutFixture(t, withPixels(metaAndGA4(marketing.ConsentRequired)), func(d *web.Deps) { d.Conversions = quiet })
	cookies, _ := checkoutCart(t, g)
	if c, _ := px(t, g.getWith(t, "/checkout", cookies).Body.String()); len(c.Events) != 1 || len(quiet.all()) != 0 {
		t.Fatalf("without consent: page %+v, server %+v", c.Events, quiet.all())
	}
	if g.getWith(t, "/checkout", append(cookies, &http.Cookie{Name: "px_c", Value: "yes"})); len(quiet.all()) != 1 {
		t.Fatalf("with consent: server %+v", quiet.all())
	}
}
