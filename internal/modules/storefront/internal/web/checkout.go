package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/arabic"
	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// CaptchaVerifier checks a Turnstile token (captcha.Turnstile).
type CaptchaVerifier interface {
	Verify(ctx context.Context, token, remoteIP string) (bool, error)
}

// Order submission limits (ADR-0022). Each subject has a soft limit,
// past which the shopper must pass a challenge, and a hard one, past
// which the order is refused. Real shoppers rarely order more than twice
// in an hour from one phone; offices and mobile carriers share IPs, so
// the per-IP limits are looser.
var (
	orderSoftIP    = ratelimit.Limit{Burst: 3, Period: 10 * time.Minute}                  //nolint:gochecknoglobals // constant policy
	orderHardIP    = ratelimit.Limit{Burst: 30, Period: 10 * time.Minute}                 //nolint:gochecknoglobals // constant policy
	orderSoftPhone = ratelimit.Limit{Burst: 2, Period: time.Hour}                         //nolint:gochecknoglobals // constant policy
	orderHardPhone = ratelimit.Limit{Burst: 6, Period: time.Hour}                         //nolint:gochecknoglobals // constant policy
	orderPerStore  = ratelimit.Limit{Burst: 6000, Period: time.Minute}                    //nolint:gochecknoglobals // constant policy
	maxOrderBytes  = int64(8 << 10)                                                       //nolint:gochecknoglobals // constant policy
	formFields     = []string{"name", "phone", "governorate", "city", "address", "notes"} //nolint:gochecknoglobals // constant list
)

// fieldErrors maps a form field to the message shown under it.
func fieldErrors() map[string]Msg {
	return map[string]Msg{"name": MsgErrName, "phone": MsgErrPhone, "governorate": MsgErrGovernorate,
		"city": MsgErrCity, "address": MsgErrAddress, "notes": MsgErrNotes}
}

// orderForm is the one-step form's state when it is rendered again.
type orderForm struct {
	action    string // "/order" or "/checkout"
	variant   ulid.ID
	qty       int
	values    map[string]string
	errors    map[string]Msg
	problem   string
	challenge string // the Turnstile site key, when the widget must show
	areas     []geo.Area
	summary   *orderSummary
	cta       string
	card      bool // card payment is offered (checkout from the cart only)
}

// payByCard reports whether the shopper chose card payment.
func (f *orderForm) payByCard() bool { return f.card && f.value("pay_method") == "card" }

func (f *orderForm) value(name string) string { return f.values[name] }

func (f *orderForm) bad(name string) bool {
	_, ok := f.errors[name]
	return ok
}

type orderSummary struct {
	lines    []summaryLine
	subtotal *money.Money
	units    int
}

type summaryLine struct {
	title, options string
	qty            int
	total          money.Money
	thumb          *thumb
}

// risk is what the limits say about a submission.
type risk int

const (
	riskNone risk = iota
	riskChallenge
	riskRefuse
)

// orderRoute serves checkout and the landing pages' one-step form; ok is
// false for other paths.
func (s *Shop) orderRoute(w http.ResponseWriter, r *http.Request, store tenant.StoreID, p *pageCtx, rest string) bool {
	switch {
	case rest == "/checkout" && isRead(r):
		s.checkoutPage(w, r, p)
	case rest == "/checkout" && r.Method == http.MethodPost:
		s.submitOrder(w, r, store, p, true)
	case rest == "/order" && r.Method == http.MethodPost:
		s.submitOrder(w, r, store, p, false)
	case rest == "/order/quote" && r.Method == http.MethodPost:
		s.quoteOrder(w, r, p)
	default:
		return false
	}
	return true
}

// checkoutPage shows the cart's summary with the order form.
func (s *Shop) checkoutPage(w http.ResponseWriter, r *http.Request, p *pageCtx) {
	if s.orders == nil {
		s.writeNotFoundPrivate(w, r, p)
		return
	}
	view, err := s.carts.Cart(r.Context(), s.cartToken(r))
	if err != nil {
		s.unavailable(w, r, err)
		return
	}
	if len(view.Lines) == 0 {
		http.Redirect(w, r, p.localize(p.l, "/cart"), http.StatusSeeOther)
		return
	}
	f := &orderForm{action: "/checkout", areas: geo.Areas(p.v.country), summary: s.cartSummary(view), card: s.cardOffered(r.Context(), false)}
	if f.card && s.cardFirst(r.Context()) {
		f.values = map[string]string{"pay_method": "card"} // the merchant's choice; the shopper can switch
	}
	if view.Subtotal != nil {
		ids := make([]string, len(view.Lines))
		for i, l := range view.Lines {
			ids[i] = l.VariantID.String()
		}
		p.event(EventInitiateCheckout, s.ids.New().String(), *view.Subtotal, ids)
	}
	s.renderOrder(w, r, p, http.StatusOK, f)
}

// submitOrder places an order from the cart (fromCart) or from a
// landing page's form. In order: same-origin, size, honeypot, limits,
// challenge when risky, then the order itself. Success redirects to the
// thank-you page (post/redirect/get).
func (s *Shop) submitOrder(w http.ResponseWriter, r *http.Request, store tenant.StoreID, p *pageCtx, fromCart bool) {
	if s.orders == nil {
		s.writeNotFoundPrivate(w, r, p)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxOrderBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	f := &orderForm{action: "/order", areas: geo.Areas(p.v.country), values: map[string]string{}, errors: map[string]Msg{}}
	for _, k := range append([]string{"key", "pay_method", "coupon"}, formFields...) {
		f.values[k] = r.PostForm.Get(k)
	}
	req := OrderRequest{Key: f.values["key"], Name: f.values["name"], Phone: f.values["phone"], Governorate: f.values["governorate"],
		City: f.values["city"], Address: f.values["address"], Notes: f.values["notes"], Coupon: strings.TrimSpace(f.values["coupon"]),
		Visit: s.visit(r, p)}

	var view app.CartView
	if fromCart {
		f.action = "/checkout"
		var err error
		if view, err = s.carts.Cart(ctx, s.cartToken(r)); err != nil {
			s.unavailable(w, r, err)
			return
		}
		if len(view.Lines) == 0 {
			http.Redirect(w, r, p.localize(p.l, "/cart"), http.StatusSeeOther)
			return
		}
		f.summary = s.cartSummary(view)
		f.card = s.cardOffered(ctx, true) // placing the order decides: never from the cache
		if f.value("pay_method") == "card" && !f.card {
			f.problem = p.l.T(MsgErrCardUnavailable)
			s.renderOrder(w, r, p, http.StatusConflict, f)
			return
		}
		req.Card = f.payByCard()
		for _, l := range view.Lines {
			if !l.Available {
				f.problem = p.l.T(MsgErrCartChanged)
				s.renderOrder(w, r, p, http.StatusConflict, f)
				return
			}
			req.Lines = append(req.Lines, OrderRequestLine{VariantID: l.VariantID, Qty: l.Qty})
		}
	} else {
		id, idErr := ulid.Parse(r.PostForm.Get("variant"))
		qty, qtyErr := strconv.Atoi(r.PostForm.Get("qty"))
		if idErr != nil || qtyErr != nil || qty < 1 || qty > 20 {
			f.problem = p.l.T(MsgErrQty)
			s.renderOrder(w, r, p, http.StatusUnprocessableEntity, f)
			return
		}
		f.variant, f.qty = id, qty
		f.summary = s.lineSummary(ctx, id, qty)
		req.Lines = []OrderRequestLine{{VariantID: id, Qty: qty}}
	}

	// A filled honeypot is a bot: refuse as we would a blocked customer,
	// without saying why.
	if r.PostForm.Get("website") != "" {
		s.log.InfoContext(ctx, "storefront: honeypot filled; order refused")
		f.problem = p.l.T(MsgErrOrderRefused)
		s.renderOrder(w, r, p, http.StatusUnprocessableEntity, f)
		return
	}
	switch s.orderRisk(r, store, req.Phone) {
	case riskRefuse:
		w.Header().Set("Retry-After", "600")
		f.problem = p.l.T(MsgErrBusy)
		s.renderOrder(w, r, p, http.StatusTooManyRequests, f)
		return
	case riskChallenge:
		if ok, msg := s.challenge(r); !ok {
			f.challenge, f.problem = s.siteKey, p.l.T(msg)
			if f.challenge == "" { // no way to verify: treat as busy
				w.Header().Set("Retry-After", "600")
				f.problem = p.l.T(MsgErrBusy)
				s.renderOrder(w, r, p, http.StatusTooManyRequests, f)
				return
			}
			s.renderOrder(w, r, p, http.StatusUnprocessableEntity, f)
			return
		}
	case riskNone:
	}

	ref, err := s.orders.PlaceOrder(ctx, req)
	var invalid *OrderInvalidError
	switch {
	case errors.As(err, &invalid):
		msgs := fieldErrors()
		if invalid.NotDelivered {
			msgs["governorate"] = MsgErrNotDelivered
		}
		if invalid.Coupon != "" {
			msgs["coupon_code"] = couponReason(invalid.Coupon)
		}
		for _, name := range invalid.Fields {
			if m, ok := msgs[name]; ok {
				if name == "coupon_code" {
					name = "coupon" // the form's field
				}
				f.errors[name] = m
			} else {
				f.problem = p.l.T(MsgErrUnavailable) // the lines: an item stopped selling
			}
		}
		if f.problem == "" {
			f.problem = p.l.T(MsgErrFix)
		}
		s.renderOrder(w, r, p, http.StatusUnprocessableEntity, f)
		return
	case errors.Is(err, ErrOrderRefused):
		f.problem = p.l.T(MsgErrOrderRefused)
		s.renderOrder(w, r, p, http.StatusUnprocessableEntity, f)
		return
	case errors.Is(err, ErrOrderOutOfStock):
		f.problem = p.l.T(MsgErrStock)
		s.renderOrder(w, r, p, http.StatusConflict, f)
		return
	case errors.Is(err, ErrOrderUnavailable):
		f.problem = p.l.T(MsgErrUnavailable)
		s.renderOrder(w, r, p, http.StatusConflict, f)
		return
	case err != nil:
		s.unavailable(w, r, err)
		return
	}
	if fromCart {
		if err = s.carts.ClearCart(ctx, s.cartToken(r)); err != nil {
			s.log.WarnContext(ctx, "storefront: clear cart after order", "err", err) // the order stands
		}
		s.setUnitsCookie(w, 0)
	}
	w.Header().Set("Cache-Control", noStore)
	thanks := p.localize(p.l, "/thank-you/"+ref)
	if req.Card {
		// The thank-you page sends the shopper on to pay (ADR-0030): a
		// redirect from here would put the API host and the gateway under
		// this form's form-action.
		thanks += "?pay=1"
	}
	http.Redirect(w, r, thanks, http.StatusSeeOther)
}

// cardOffered reports whether checkout offers card payment. The page
// reads it from the cache (TTL, and dropped when the store connects or
// disconnects a gateway); placing the order asks (fresh). A failed check
// offers only cash on delivery: the order can still be placed.
func (s *Shop) cardOffered(ctx context.Context, fresh bool) bool {
	if s.payments == nil {
		return false
	}
	store, err := tenant.FromContext(ctx)
	if err != nil {
		return false
	}
	key := s.caches.prefix(store) + "cards"
	if !fresh {
		if on, ok := s.caches.cards.Get(key); ok {
			return on
		}
	}
	on, err := s.payments.CardEnabled(ctx)
	if err != nil {
		s.log.WarnContext(ctx, "storefront: card payment check", "err", err)
		return false
	}
	s.caches.cards.Add(key, on)
	return on
}

// cardFirst reports whether checkout starts on card (the merchant's
// choice, ADR-0036), cached like cardOffered; a failed check starts on
// cash on delivery.
func (s *Shop) cardFirst(ctx context.Context) bool {
	store, err := tenant.FromContext(ctx)
	if err != nil || s.payments == nil {
		return false
	}
	key := s.caches.prefix(store) + "card-first"
	if on, ok := s.caches.cards.Get(key); ok {
		return on
	}
	on, err := s.payments.CardDefault(ctx)
	if err != nil {
		s.log.WarnContext(ctx, "storefront: card default check", "err", err)
		return false
	}
	s.caches.cards.Add(key, on)
	return on
}

// orderRisk applies the order limits. A limiter failure asks for a
// challenge rather than letting every submission through or refusing
// every one.
func (s *Shop) orderRisk(r *http.Request, store tenant.StoreID, rawPhone string) risk {
	if s.limiter == nil {
		return riskNone
	}
	ctx := r.Context()
	ip, ph := "order:ip:"+s.clientIP(r), "order:phone:"+store.String()+":"+phoneKey(rawPhone)
	result := riskNone
	for _, c := range []struct {
		key  string
		lim  ratelimit.Limit
		over risk
	}{
		{"order:store:" + store.String(), orderPerStore, riskRefuse},
		{ip + ":hard", orderHardIP, riskRefuse},
		{ph + ":hard", orderHardPhone, riskRefuse},
		{ip + ":soft", orderSoftIP, riskChallenge},
		{ph + ":soft", orderSoftPhone, riskChallenge},
	} {
		res, err := s.limiter.Allow(ctx, c.key, c.lim)
		switch {
		case err != nil:
			s.log.WarnContext(ctx, "storefront: order limiter unavailable", "err", err)
			return riskChallenge
		case !res.Allowed && c.over == riskRefuse:
			return riskRefuse
		case !res.Allowed:
			result = riskChallenge
		}
	}
	return result
}

// challenge checks the Turnstile answer posted with the form. The
// message explains a failure to the shopper.
func (s *Shop) challenge(r *http.Request) (bool, Msg) {
	token := r.PostForm.Get("cf-turnstile-response")
	if s.captcha == nil || token == "" {
		return false, MsgVerifyHuman
	}
	ok, err := s.captcha.Verify(r.Context(), token, s.clientIP(r))
	if err != nil {
		s.log.WarnContext(r.Context(), "storefront: captcha unavailable", "err", err)
		return false, MsgErrCaptcha
	}
	if !ok {
		return false, MsgErrCaptcha
	}
	return true, 0
}

// phoneKey is a rate-limit key for a typed phone: its last 9 digits
// (Arabic-Indic digits read as ASCII), so "010…", "+2010…" and
// "٠١٠…" count as one number. It never needs to be a valid number.
func phoneKey(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if d := arabic.ASCIIDigit(r); d >= '0' && d <= '9' {
			b.WriteRune(d)
		}
	}
	d := b.String()
	if len(d) > 9 {
		d = d[len(d)-9:]
	}
	return d
}

// lineSummary describes a landing page order's one line, or nothing if
// the variant is no longer for sale (the order then says so).
func (s *Shop) lineSummary(ctx context.Context, id ulid.ID, qty int) *orderSummary {
	vs, err := s.catalog.SellableVariants(ctx, []ulid.ID{id})
	if err != nil || len(vs) == 0 {
		return nil
	}
	v := vs[0]
	total, err := v.Price.MulInt(int64(qty))
	if err != nil {
		return nil
	}
	return &orderSummary{lines: []summaryLine{{title: v.ProductTitle, options: joinOptions(v.OptionValues), qty: qty, total: total,
		thumb: s.thumbOf(v.ImageKey)}}, subtotal: &total, units: qty}
}

// renderOrder renders the form page; with a challenge, its response
// alone allows Turnstile's script and frame.
func (s *Shop) renderOrder(w http.ResponseWriter, r *http.Request, p *pageCtx, status int, f *orderForm) {
	if f.areas == nil {
		f.areas = geo.Areas(p.v.country)
	}
	p.title, p.noindex = p.l.T(MsgCheckout)+" | "+p.v.name, true
	p.challenge = f.challenge != ""
	p.head, p.back, p.soft, p.cssURL, p.pageJS = headCheckout, "/cart", true, s.assets.checkoutURL, s.assets.checkoutJS
	s.writePrivate(w, r, p, status, orderPage(p, f))
}
