package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/web"
)

// TestCheckoutCoupon: checkout has the coupon field and a hidden
// discount row; a code that applies takes its discount off the quote's
// total and says so; one that does not says why, with the totals
// without it.
func TestCheckoutCoupon(t *testing.T) {
	t.Parallel()
	f := newCheckoutFixture(t)
	cookies, _ := checkoutCart(t, f)
	body := f.getWith(t, "/checkout", cookies).Body.String()
	wantContains(t, body, `<label for="o-coupon">كود الخصم (اختياري)</label>`, `name="coupon" id="o-coupon"`, `data-coupon-apply>تطبيق</button>`,
		`<p class="row discount" data-discount-row hidden><span>الخصم</span><span data-amt="discount"></span></p>`, `data-coupon-msg`)

	quote := func(code string) map[string]any {
		t.Helper()
		code0, q := quoteOf(t, f, "/order/quote", url.Values{"governorate": {"EG-C"}, "coupon": {code}}, cookies)
		if code0 != http.StatusOK {
			t.Fatalf("quote %q = %d", code, code0)
		}
		return q
	}
	q := quote(" save10 ")
	a := q["amounts"].(map[string]any)
	c := q["coupon"].(map[string]any)
	if a["discount"] != "− 30 ج.م" || a["total"] != "330 ج.م" || c["ok"] != true || c["code"] != "SAVE10" || c["message"] != "تم تطبيق الكود" {
		t.Fatalf("applied = %v", q)
	}
	for code, msg := range map[string]string{"OLD": "انتهت صلاحية هذا الكود", "NOPE": "هذا الكود غير صحيح"} {
		q = quote(code)
		a, c = q["amounts"].(map[string]any), q["coupon"].(map[string]any)
		if a["discount"] != nil || a["total"] != "360 ج.م" || c["ok"] != false || c["message"] != msg {
			t.Errorf("%s = %v", code, q)
		}
	}
	if q = quote(""); q["coupon"] != nil || q["amounts"].(map[string]any)["discount"] != nil {
		t.Fatalf("no code = %v", q)
	}
	_, en := quoteOf(t, f, "/en/order/quote", url.Values{"governorate": {"EG-C"}, "coupon": {"OLD"}}, cookies)
	if en["coupon"].(map[string]any)["message"] != "This code has expired" {
		t.Fatalf("English = %v", en)
	}
}

// TestPlaceOrderWithCoupon: the code goes with the order; a code the
// order refuses comes back on the form, the code kept, with why.
func TestPlaceOrderWithCoupon(t *testing.T) {
	t.Parallel()
	f := newCheckoutFixture(t)
	cookies, form := checkoutCart(t, f)
	form.Set("coupon", " save10 ")
	if rec := f.post(t, "/checkout", form, cookies, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("order = %d %s", rec.Code, rec.Body)
	}
	if reqs := f.orders.placed(); len(reqs) != 1 || reqs[0].Coupon != "save10" {
		t.Fatalf("request = %+v", reqs)
	}

	f.orders.log.mu.Lock()
	f.orders.log.err = &web.OrderInvalidError{Fields: []string{"coupon_code"}, Coupon: "per_customer"}
	f.orders.log.mu.Unlock()
	cookies, form = checkoutCart(t, f) // the first order emptied the cart
	form.Set("coupon", "ONCE")
	form.Set("key", "form-key-2")
	rec := f.post(t, "/checkout", form, cookies, nil)
	body := rec.Body.String()
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("refused = %d", rec.Code)
	}
	wantContains(t, body, `class="card-box fld coupon bad"`, `value="ONCE"`, "استخدمت هذا الكود من قبل", "راجع البيانات المظللة.")
}

// TestThankYouDiscount: the thank-you page shows the coupon's discount.
func TestThankYouDiscount(t *testing.T) {
	t.Parallel()
	f := newCheckoutFixture(t)
	f.orders.log.mu.Lock()
	f.orders.log.summary = &web.OrderSummary{Ref: "IFT-1001", Code: "IFT-1001", Subtotal: egp(30000), Shipping: egp(5000), CODFee: egp(1000),
		Discount: egp(3000), CouponCode: "SAVE10", Total: egp(33000), FirstName: "منى"}
	f.orders.log.mu.Unlock()
	body := f.get(t, "/thank-you/IFT-1001").Body.String()
	wantContains(t, body, `<p class="row discount"><span>الخصم <bdi dir="ltr">SAVE10</bdi></span><span>− 30 ج.م</span></p>`, "330 ج.م")
	f.orders.log.mu.Lock()
	f.orders.log.summary = nil
	f.orders.log.mu.Unlock()
	if strings.Contains(f.get(t, "/thank-you/IFT-1001").Body.String(), "row discount") {
		t.Fatal("a discount row without a discount")
	}
}
