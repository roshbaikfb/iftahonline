package web

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/authtoken"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Carts is the cart's use cases (app.Service).
type Carts interface {
	AddToCart(ctx context.Context, token authtoken.Token, variantID ulid.ID, qty int) (authtoken.Token, error)
	UpdateCartLine(ctx context.Context, token authtoken.Token, variantID ulid.ID, qty int) error
	Cart(ctx context.Context, token authtoken.Token) (app.CartView, error)
	CartUnits(ctx context.Context, token authtoken.Token) (int, error)
	ClearCart(ctx context.Context, token authtoken.Token) error
}

var _ Carts = (*app.Service)(nil)

// OrderSummary is what the thank-you page shows of a placed order. The
// orders module provides it.
type OrderSummary struct {
	Ref  string // the order's ID, in the thank-you URL
	Code string // its short code, shown to the shopper
	// Card orders are paid online: PaymentStatus is awaiting until the
	// gateway says paid. Cancelled is true once the order is cancelled
	// (an unpaid card order is, after an hour).
	Card          bool
	PaymentStatus string
	Cancelled     bool
	Lines         []OrderLine
	Subtotal      money.Money
	Shipping      money.Money
	CODFee        money.Money
	Discount      money.Money // the coupon's; zero without one
	CouponCode    string
	Total         money.Money
	// What the thank-you page says to the shopper: the first name only and
	// where it goes, never the street (the page's link may be shared).
	FirstName   string
	Governorate string // ISO 3166-2
	City        string
	Days        *DeliveryDays // how long delivery takes, when the store says
}

// OrderLine is one line of an OrderSummary.
type OrderLine struct {
	VariantID ulid.ID // for the line's picture
	Title     string
	Qty       int
	Total     money.Money
}

// Orders places shoppers' orders and finds them again for the thank-you
// page, in the store in ctx. The storefront module adapts the orders
// module to it.
type Orders interface {
	// OrderSummary finds a placed order by its reference;
	// ErrOrderNotFound when there is none.
	OrderSummary(ctx context.Context, ref string) (OrderSummary, error)
	// PlaceOrder places an order and returns its reference (that of the
	// earlier order for a repeated submit). Errors: *OrderInvalidError,
	// ErrOrderRefused, ErrOrderOutOfStock, ErrOrderUnavailable.
	PlaceOrder(ctx context.Context, req OrderRequest) (string, error)
	// Quote prices an order before it is placed: shipping and COD fee to
	// an address. Errors: *OrderInvalidError, ErrOrderUnavailable.
	Quote(ctx context.Context, req QuoteRequest) (QuoteView, error)
	// Track finds the order a shopper names by its code and phone;
	// ErrOrderNotFound whichever part was wrong.
	Track(ctx context.Context, code, phone string) (Tracking, error)
	// Bestsellers returns the store's products by units sold in
	// confirmed orders over the last 30 days, most first.
	Bestsellers(ctx context.Context, limit int) ([]ulid.ID, error)
}

// Tracking is what order tracking shows of an order: where it stands and
// its parcel, never the address, the name or the lines.
type Tracking struct {
	Code          string
	Status        string // the order's status (pending, confirmed, ready_to_ship, shipped, delivered, cancelled, failed_delivery, returned)
	PlacedAt      time.Time
	Card          bool
	PaymentStatus string
	Items         int
	Total         money.Money
	Parcel        *Parcel // nil until a courier has it
}

// Parcel is an order's shipment as a shopper sees it.
type Parcel struct {
	Carrier string // the carrier's code (bosta, fake)
	Number  string // the tracking number
	Status  string // created, in_transit, out_for_delivery, delivered, failed_attempt, returned
}

// QuoteRequest is the part of the order form a quote needs.
type QuoteRequest struct {
	Governorate, City string
	Card              bool // paid online: no cash-on-delivery fee
	Lines             []OrderRequestLine
	Coupon            string // a coupon code; may be empty
}

// QuoteView is what an order would cost.
type QuoteView struct {
	Deliverable  bool
	FreeShipping bool
	Subtotal     money.Money
	Shipping     money.Money
	CODFee       money.Money
	Total        money.Money
	FreeOver     *money.Money  // the free-shipping threshold in force; nil: none
	Days         *DeliveryDays // how long delivery takes; nil: the store did not say
	// Discount is the coupon's (zero without one), Coupon its code.
	Discount money.Money
	Coupon   string
}

// DeliveryDays is how long delivery takes, in days.
type DeliveryDays struct {
	Min, Max int
}

// OrderRequest is the one-step form as submitted.
type OrderRequest struct {
	Key                                            string // idempotency key; may be empty
	Name, Phone, Governorate, City, Address, Notes string
	Card                                           bool // paid online, not cash on delivery
	Lines                                          []OrderRequestLine
	Coupon                                         string // a coupon code; may be empty
	Visit                                          Visit  // where the shopper came from (ADR-0043)
}

// Payment states an OrderSummary reports.
const (
	PaymentAwaiting = "awaiting"
	PaymentPaid     = "paid"
)

// Payments takes card payments through the platform's API host
// (ADR-0030); nil means cash on delivery only.
type Payments interface {
	// CardEnabled reports whether the store in ctx takes cards.
	CardEnabled(ctx context.Context) (bool, error)
	// CardDefault reports whether its checkout starts on card (ADR-0036).
	CardDefault(ctx context.Context) (bool, error)
	// PayURL is where a card order's shopper pays; back is the absolute
	// thank-you URL they return to.
	PayURL(store, orderRef, back string) string
}

// OrderRequestLine is one variant and quantity.
type OrderRequestLine struct {
	VariantID ulid.ID
	Qty       int
}

// Order placement errors. Shoppers see what to do, never why an order
// was refused (a blocked customer only reads ErrOrderRefused's message).
var (
	ErrOrderRefused     = errors.New("web: order refused")
	ErrOrderOutOfStock  = errors.New("web: not enough stock")
	ErrOrderUnavailable = errors.New("web: an item is no longer for sale")
)

// OrderInvalidError names the form fields that need fixing.
type OrderInvalidError struct {
	Fields []string // name, phone, governorate, city, address, notes, lines
	// NotDelivered: the store does not deliver to the governorate.
	NotDelivered bool
	// Coupon is why the coupon code does not apply (not_found, inactive,
	// not_started, expired, min_subtotal, used_up, per_customer,
	// changed); "" when it does.
	Coupon string
}

func (e *OrderInvalidError) Error() string {
	return "web: invalid order: " + strings.Join(e.Fields, ", ")
}

// ErrOrderNotFound means no order has that reference.
var ErrOrderNotFound = errors.New("web: order not found")

// Limiter is the rate limiter (ratelimit.Limiter).
type Limiter interface {
	Allow(ctx context.Context, key string, lim ratelimit.Limit) (ratelimit.Result, error)
}

// Cart limits on public writes (CLAUDE.md: rate-limit public endpoints
// per store and per IP).
var (
	cartPerIP    = ratelimit.Limit{Burst: 30, Period: time.Minute}   //nolint:gochecknoglobals // constant policy
	cartPerStore = ratelimit.Limit{Burst: 1200, Period: time.Minute} //nolint:gochecknoglobals // constant policy
	maxFormBytes = int64(4 << 10)                                    //nolint:gochecknoglobals // constant policy
	cartMaxAge   = int(domain.CartTTL / time.Second)                 //nolint:gochecknoglobals // constant policy
	noStore      = "private, no-store"                               //nolint:gochecknoglobals // constant policy
)

func (s *Shop) cookieName() string {
	if s.scheme == "https" {
		return "__Host-cart" // Secure, host-only, path /: cannot be set by a sibling subdomain
	}
	return "cart"
}

func (s *Shop) cartToken(r *http.Request) authtoken.Token {
	c, err := r.Cookie(s.cookieName())
	if err != nil {
		return ""
	}
	t, ok := authtoken.Parse(c.Value)
	if !ok {
		return ""
	}
	return t
}

func (s *Shop) setCartCookie(w http.ResponseWriter, t authtoken.Token) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is off only on plain-HTTP development hosts
		Name: s.cookieName(), Value: string(t), Path: "/", MaxAge: cartMaxAge,
		HttpOnly: true, Secure: s.scheme == "https", SameSite: http.SameSiteLaxMode,
	})
}

// unitsCookie is the cart's unit count for the header badge. Pages are
// the same for everyone (edge-cached), so the count cannot be in them;
// the script reads this cookie instead of asking the origin on every
// page view. It holds no secret.
const unitsCookie = "cart_n"

func (s *Shop) setUnitsCookie(w http.ResponseWriter, n int) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // readable by the page script on purpose: a count, not a secret
		Name: unitsCookie, Value: strconv.Itoa(n), Path: "/", MaxAge: cartMaxAge,
		Secure: s.scheme == "https", SameSite: http.SameSiteLaxMode,
	})
}

// sameOrigin rejects cross-site form posts (CSRF): the browser says where
// the request comes from, and it must be this store's host.
func sameOrigin(r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" && o != "null" {
		u, err := url.Parse(o)
		return err == nil && u.Host == r.Host
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin":
		return true
	case "":
		if ref := r.Referer(); ref != "" {
			u, err := url.Parse(ref)
			return err == nil && u.Host == r.Host
		}
	}
	return false
}

// allow applies the per-IP and per-store limits. If Redis is down the
// request goes through: a shop that cannot take orders loses more than a
// limit protects.
func (s *Shop) allow(r *http.Request, store tenant.StoreID) bool {
	if s.limiter == nil {
		return true
	}
	for _, c := range []struct {
		key string
		lim ratelimit.Limit
	}{{"cart:ip:" + s.clientIP(r), cartPerIP}, {"cart:store:" + store.String(), cartPerStore}} {
		res, err := s.limiter.Allow(r.Context(), c.key, c.lim)
		if err != nil {
			s.log.WarnContext(r.Context(), "storefront: rate limiter unavailable", "err", err)
			return true
		}
		if !res.Allowed {
			return false
		}
	}
	return true
}

// cartRoute serves the cart and thank-you routes; ok is false for other
// paths.
func (s *Shop) cartRoute(w http.ResponseWriter, r *http.Request, store tenant.StoreID, p *pageCtx, rest string) bool {
	switch {
	case rest == "/cart" && r.Method == http.MethodPost:
		s.cartWrite(w, r, store, p, true, func(tok authtoken.Token, id ulid.ID, qty int) (authtoken.Token, error) {
			return s.carts.AddToCart(r.Context(), tok, id, qty)
		})
	case rest == "/cart/update" && r.Method == http.MethodPost:
		s.cartWrite(w, r, store, p, false, func(tok authtoken.Token, id ulid.ID, qty int) (authtoken.Token, error) {
			return tok, s.carts.UpdateCartLine(r.Context(), tok, id, qty)
		})
	case rest == "/cart" && isRead(r):
		s.cartPage(w, r, p, http.StatusOK, 0)
	case rest == "/track":
		s.trackRoute(w, r, store, p)
	case len(rest) > len("/o/") && rest[:len("/o/")] == "/o/":
		s.confirmRoute(w, r, store, p, rest[len("/o/"):])
	case len(rest) > len("/thank-you/") && rest[:len("/thank-you/")] == "/thank-you/" && isRead(r):
		s.thankYou(w, r, p, rest[len("/thank-you/"):])
	default:
		return false
	}
	return true
}

func isRead(r *http.Request) bool { return r.Method == http.MethodGet || r.Method == http.MethodHead }

// cartWrite runs a form post that changes the cart, then sends the
// shopper to the cart (post/redirect/get). Errors show on the cart page.
// When choose is set, a form naming a product and its option values
// instead of a variant (the product page without JavaScript) is resolved
// to the variant they name.
func (s *Shop) cartWrite(w http.ResponseWriter, r *http.Request, store tenant.StoreID, p *pageCtx, choose bool,
	do func(authtoken.Token, ulid.ID, int) (authtoken.Token, error)) {
	if !sameOrigin(r) {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	if !s.allow(r, store) {
		w.Header().Set("Retry-After", "60")
		s.cartPage(w, r, p, http.StatusTooManyRequests, MsgErrBusy)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	id, idErr := ulid.Parse(r.PostForm.Get("variant"))
	if idErr != nil && choose && r.PostForm.Get("variant") == "" {
		chosen, ok, err := s.chosenVariant(r.Context(), r.PostForm)
		if err != nil {
			s.unavailable(w, r, err)
			return
		}
		if ok {
			id, idErr = chosen, nil
		}
	}
	qty, qtyErr := strconv.Atoi(r.PostForm.Get("qty"))
	if idErr != nil {
		s.cartPage(w, r, p, http.StatusUnprocessableEntity, MsgErrUnavailable)
		return
	}
	if qtyErr != nil {
		s.cartPage(w, r, p, http.StatusUnprocessableEntity, MsgErrQty)
		return
	}
	old := s.cartToken(r)
	tok, err := do(old, id, qty)
	if err != nil {
		msg, status := cartError(err)
		if status == 0 {
			s.unavailable(w, r, err)
			return
		}
		s.cartPage(w, r, p, status, msg)
		return
	}
	if tok != "" && tok != old {
		s.setCartCookie(w, tok)
	}
	if n, err := s.carts.CartUnits(r.Context(), tok); err == nil {
		s.setUnitsCookie(w, n)
	}
	if choose { // an add, not an update: the cart page reports it
		s.setAdded(w, p, id, qty)
	}
	w.Header().Set("Cache-Control", noStore)
	http.Redirect(w, r, p.localize(p.l, "/cart"), http.StatusSeeOther)
}

// cartError maps a cart error to its message and status; 0 means an
// unexpected failure.
func cartError(err error) (Msg, int) {
	switch {
	case errors.Is(err, app.ErrInsufficientStock):
		return MsgErrStock, http.StatusConflict
	case errors.Is(err, app.ErrVariantUnavailable):
		return MsgErrUnavailable, http.StatusConflict
	case errors.Is(err, app.ErrCartFull):
		return MsgErrCartFull, http.StatusConflict
	}
	var e *errs.Error
	if errors.As(err, &e) && e.Kind == errs.KindValidation {
		return MsgErrQty, http.StatusUnprocessableEntity
	}
	return 0, 0
}

// cartPage renders the shopper's cart; never cached, never indexed.
func (s *Shop) cartPage(w http.ResponseWriter, r *http.Request, p *pageCtx, status int, problem Msg) {
	view, err := s.carts.Cart(r.Context(), s.cartToken(r))
	if err != nil {
		s.unavailable(w, r, err)
		return
	}
	p.title, p.noindex = p.l.T(MsgCart)+" | "+p.v.name, true
	s.setUnitsCookie(w, view.Units) // lines may have dropped out since
	msg := ""
	if problem != 0 {
		msg = p.l.T(problem)
	}
	p.soft, p.cssURL = true, s.assets.checkoutURL
	if id, variant, qty, ok := takeAdded(w, r); ok {
		for _, l := range view.Lines {
			if l.VariantID == variant {
				if value, mulErr := l.Price.MulInt(int64(qty)); mulErr == nil {
					p.event(EventAddToCart, id, value, []string{variant.String()})
					s.track(r, p, EventAddToCart, id, value, []string{variant.String()})
				}
				break
			}
		}
	}
	s.writePrivate(w, r, p, status, cartPage(p, view, s.cartLines(view), msg))
}

func (s *Shop) thankYou(w http.ResponseWriter, r *http.Request, p *pageCtx, ref string) {
	if s.orders == nil || len(ref) > 64 {
		s.writeNotFoundPrivate(w, r, p)
		return
	}
	o, err := s.orders.OrderSummary(r.Context(), ref)
	if errors.Is(err, ErrOrderNotFound) {
		s.writeNotFoundPrivate(w, r, p)
		return
	}
	if err != nil {
		s.unavailable(w, r, err)
		return
	}
	p.title, p.noindex = p.l.T(MsgThankYou)+" | "+p.v.name, true
	payURL := ""
	if o.Card && o.PaymentStatus == PaymentAwaiting && !o.Cancelled && s.payments != nil {
		// Not paid yet: the webhook may be seconds away, so the page looks
		// again by itself; or the shopper pays (again) now.
		payURL = s.payments.PayURL(p.store, o.Ref, p.base+p.localize(p.l, "/thank-you/"+o.Ref))
		p.refresh = 10
		if r.URL.Query().Get("pay") == "1" { // just placed: on to the payment
			p.refresh, p.refreshTo = 1, payURL
		}
	}
	p.head, p.soft, p.cssURL = headThanks, true, s.assets.checkoutURL
	if !o.Cancelled {
		ids := make([]string, len(o.Lines))
		for i, l := range o.Lines {
			ids[i] = l.VariantID.String()
		}
		if e := p.event(EventPurchase, purchaseEventID(o.Ref), o.Total, ids); e != nil {
			e.Ref = o.Ref
		}
	}
	s.writePrivate(w, r, p, http.StatusOK, thankYouPage(p, s.thanksView(r.Context(), p, o, payURL)))
}

func (s *Shop) writeNotFoundPrivate(w http.ResponseWriter, r *http.Request, p *pageCtx) {
	p.title, p.noindex = p.l.T(MsgNotFoundTitle)+" | "+p.v.name, true
	s.writePrivate(w, r, p, http.StatusNotFound, notFoundPage(p))
}

// writePrivate renders a page for one shopper: no shared caching.
func (s *Shop) writePrivate(w http.ResponseWriter, r *http.Request, p *pageCtx, status int, body templ.Component) {
	var buf bytes.Buffer
	if err := layout(p, body).Render(r.Context(), &buf); err != nil {
		s.unavailable(w, r, err)
		return
	}
	c := cached{status: status, body: buf.Bytes(), styleHash: p.v.styleHash, px: p.v.pxSources(), language: p.l, challenge: p.challenge}
	if acceptsGzip(r) {
		c.gz = compress(c.body)
	}
	s.writeHeaders(w, c)
	w.Header().Set("Cache-Control", noStore)
	w.Header().Del("Cache-Tag")
	s.writeBody(w, r, c)
}
