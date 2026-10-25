package web_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/web"
)

// fakePayments takes cards when on; its pay URL names the store, the
// order and the way back. asked counts the checks, when set.
type fakePayments struct {
	on    bool
	err   error
	asked *atomic.Int64
}

func (p fakePayments) CardEnabled(context.Context) (bool, error) {
	if p.asked != nil {
		p.asked.Add(1)
	}
	return p.on, p.err
}

func (fakePayments) PayURL(store, orderRef, back string) string {
	return "https://app.test/v1/pay/" + store + "/" + orderRef + "?return=" + url.QueryEscape(back)
}

func withCards(p fakePayments) func(*web.Deps) { return func(d *web.Deps) { d.Payments = p } }

// checkoutCart puts two shirts in the cart and returns its cookies and
// the checkout form.
func checkoutCart(t *testing.T, f checkoutFixture) ([]*http.Cookie, url.Values) {
	t.Helper()
	add := f.post(t, "/cart", url.Values{"variant": {f.shirt.Variants[0].ID.String()}, "qty": {"2"}}, nil, nil)
	form := f.orderForm()
	form.Del("variant")
	form.Del("qty")
	return []*http.Cookie{cookie(add, "__Host-cart")}, form
}

// TestCardCheckout: checkout offers the card only when the store takes
// cards; a card order goes to the thank-you page, which sends the
// shopper on to pay (ADR-0030).
func TestCardCheckout(t *testing.T) {
	t.Parallel()
	f := newCheckoutFixture(t, withCards(fakePayments{on: true}))
	cookies, form := checkoutCart(t, f)
	body := f.getWith(t, "/checkout", cookies).Body.String()
	wantContains(t, body, `name="pay_method" value="cod" checked`, `name="pay_method" value="card"`,
		"<strong>بطاقة بنكية</strong><small>Visa · Mastercard · ميزة — بدون رسوم إضافية</small>", "لا نرى بيانات بطاقتك",
		// cash on delivery is chosen unless the shopper picks the card
		`<span data-secure-text>تدفع للمندوب عند الاستلام</span>`)
	if strings.Contains(body, "الدفع نقدًا عند الاستلام. سنتصل بك") {
		t.Fatal("the COD-only note is shown with the choice")
	}

	form.Set("pay_method", "card")
	rec := f.post(t, "/checkout", form, cookies, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/thank-you/IFT-1001?pay=1" {
		t.Fatalf("card order: %d %v", rec.Code, rec.Header())
	}
	if reqs := f.orders.placed(); len(reqs) != 1 || !reqs[0].Card {
		t.Fatalf("placed %+v, want a card order", reqs)
	}

	cookies, form = checkoutCart(t, f)
	form.Set("pay_method", "cod")
	if rec = f.post(t, "/checkout", form, cookies, nil); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/thank-you/IFT-1001" {
		t.Fatalf("COD order: %d %v", rec.Code, rec.Header())
	}
	if reqs := f.orders.placed(); len(reqs) != 2 || reqs[1].Card {
		t.Fatalf("placed %+v, want a COD order", reqs)
	}
}

func TestCardUnavailable(t *testing.T) {
	t.Parallel()
	for name, p := range map[string]*fakePayments{"no gateway": {on: false}, "check failed": {err: errors.New("db down")}, "no payments": nil} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var f checkoutFixture
			if p == nil {
				f = newCheckoutFixture(t)
			} else {
				f = newCheckoutFixture(t, withCards(*p))
			}
			cookies, form := checkoutCart(t, f)
			body := f.getWith(t, "/checkout", cookies).Body.String()
			if strings.Contains(body, `name="pay_method"`) || !strings.Contains(body, `<p class="cod-only"><strong>الدفع عند الاستلام</strong>`) {
				t.Fatal("card offered by a store that does not take cards")
			}
			// A form sent with card anyway (an old page) is refused, not
			// placed as cash on delivery behind the shopper's back.
			form.Set("pay_method", "card")
			rec := f.post(t, "/checkout", form, cookies, nil)
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "الدفع بالبطاقة غير متاح حاليًا") || len(f.orders.placed()) != 0 {
				t.Fatalf("card refused: %d, placed %+v", rec.Code, f.orders.placed())
			}
		})
	}
}

func TestCardThankYou(t *testing.T) {
	t.Parallel()
	f := newCheckoutFixture(t, withCards(fakePayments{on: true}))
	set := func(s web.OrderSummary) {
		s.Ref, s.Code, s.Subtotal, s.Shipping, s.CODFee, s.Total = "IFT-1001", "IFT-1001", egp(30000), egp(5000), egp(0), egp(35000)
		f.orders.log.mu.Lock()
		f.orders.log.summary = &s
		f.orders.log.mu.Unlock()
	}
	set(web.OrderSummary{Card: true, PaymentStatus: web.PaymentAwaiting})
	body := f.get(t, "/thank-you/IFT-1001?pay=1").Body.String()
	wantContains(t, body, `<meta http-equiv="refresh" content="1;url=https://app.test/v1/pay/`, "return=https%3A%2F%2Fmona.iftahonline.com%2Fthank-you%2FIFT-1001",
		"ادفع الآن", "لم يصلنا الدفع بعد")
	body = f.get(t, "/thank-you/IFT-1001").Body.String()
	wantContains(t, body, `<meta http-equiv="refresh" content="10">`, "ادفع الآن")

	set(web.OrderSummary{Card: true, PaymentStatus: "paid"})
	body = f.get(t, "/thank-you/IFT-1001?pay=1").Body.String()
	wantContains(t, body, `<span class="paid-chip">تم الدفع بالبطاقة · 350 ج.م</span>`, "تم تلقائياً بعد الدفع")
	if strings.Contains(body, "http-equiv") || strings.Contains(body, "ادفع الآن") {
		t.Fatal("a paid order still sends the shopper to pay")
	}

	set(web.OrderSummary{Card: true, PaymentStatus: web.PaymentAwaiting, Cancelled: true})
	body = f.get(t, "/thank-you/IFT-1001").Body.String()
	wantContains(t, body, "انتهت مهلة الدفع")
	if strings.Contains(body, "http-equiv") || strings.Contains(body, "ادفع الآن") {
		t.Fatal("a cancelled order offers to pay")
	}

	set(web.OrderSummary{PaymentStatus: "unpaid"}) // cash on delivery
	if body = f.get(t, "/thank-you/IFT-1001?pay=1").Body.String(); strings.Contains(body, "http-equiv") || strings.Contains(body, "تم الدفع") {
		t.Fatal("a COD order shows card payment")
	}
}

func TestCardQuote(t *testing.T) {
	t.Parallel()
	f := newCheckoutFixture(t, withCards(fakePayments{on: true}))
	cookies, _ := checkoutCart(t, f)
	for method, want := range map[string]string{"card": "الإجمالي: 350 ج.م", "cod": "الإجمالي: 360 ج.م"} {
		rec := f.post(t, "/order/quote", url.Values{"governorate": {"EG-C"}, "pay_method": {method}}, cookies, nil)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s quote: %d %s, want %q", method, rec.Code, rec.Body.String(), want)
		}
	}
}

// TestCardCheckIsCached: the checkout page asks whether the store takes
// cards once per cache period, not on every view; placing an order asks
// every time (it decides), and an invalidation (the store connected or
// disconnected a gateway) asks again at once.
func TestCardCheckIsCached(t *testing.T) {
	t.Parallel()
	asked := &atomic.Int64{}
	f := newCheckoutFixture(t, withCards(fakePayments{on: true, asked: asked}))
	cookies, form := checkoutCart(t, f)
	for range 3 {
		if rec := f.getWith(t, "/checkout", cookies); rec.Code != http.StatusOK {
			t.Fatalf("checkout = %d", rec.Code)
		}
	}
	if n := asked.Load(); n != 1 {
		t.Fatalf("asked %d times for 3 views, want 1", n)
	}
	form.Set("pay_method", "card")
	f.post(t, "/checkout", form, cookies, nil)
	if n := asked.Load(); n != 2 {
		t.Fatalf("placing the order asked %d times in all, want 2 (it always asks)", n)
	}
	f.shop.Invalidate(f.store)
	cookies, _ = checkoutCart(t, f)
	f.getWith(t, "/checkout", cookies)
	if n := asked.Load(); n != 3 {
		t.Fatalf("after an invalidation: asked %d times, want 3", n)
	}
}
