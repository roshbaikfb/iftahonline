package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// quotePerIP bounds quote requests: a shopper changes the governorate or
// city a few times; a script asking for every price does not need us.
var quotePerIP = ratelimit.Limit{Burst: 60, Period: time.Minute} //nolint:gochecknoglobals // constant policy

// quoteResponse is what the order form's script shows under the form,
// and the product page's delivery box its two lines.
type quoteResponse struct {
	Text        string `json:"text"`               // empty: show nothing
	Deliverable bool   `json:"deliverable"`        // false: Text says the store does not deliver there
	Delivery    string `json:"delivery,omitempty"` // "التوصيل إلى القاهرة خلال 2–3 أيام"
	Shipping    string `json:"shipping,omitempty"` // "الشحن 50 ج.م · مجاني فوق 1,000 ج.م"
	// Amounts are checkout's totals card, each on its own line.
	Amounts *quoteAmounts `json:"amounts,omitempty"`
}

type quoteAmounts struct {
	Subtotal  string `json:"subtotal"`
	ShipLabel string `json:"ship_label"`         // "الشحن إلى القاهرة"
	Shipping  string `json:"shipping"`           // "50 ج.م" or "مجاني"
	CODFee    string `json:"cod_fee,omitempty"`  // absent: no fee (or paid by card)
	Total     string `json:"total"`              //
	CODNote   string `json:"cod_note,omitempty"` // the cash choice's note with its fee
}

// quoteOrder answers the order form's script (POST /order/quote) with
// what the order would cost delivered to the chosen governorate and city:
// one line of text in the page's language, from the same quote the order
// will be placed with (ADR-0025). The landing page's form sends its
// variant and quantity; checkout's quote is of the cart. The form's
// contact fields are never sent. The answer is private and never cached.
func (s *Shop) quoteOrder(w http.ResponseWriter, r *http.Request, p *pageCtx) {
	if s.orders == nil {
		s.writeNotFoundPrivate(w, r, p)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	w.Header().Set("Cache-Control", noStore)
	r.Body = http.MaxBytesReader(w, r.Body, 2<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	if s.limiter != nil {
		res, err := s.limiter.Allow(ctx, "quote:ip:"+s.clientIP(r), quotePerIP)
		switch {
		case err != nil:
			s.log.WarnContext(ctx, "storefront: quote limiter unavailable", "err", err) // a quote changes nothing: answer it
		case !res.Allowed:
			w.Header().Set("Retry-After", strconv.Itoa(int(res.RetryAfter.Seconds())+1))
			http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
			return
		}
	}

	req := QuoteRequest{Governorate: r.PostForm.Get("governorate"), City: r.PostForm.Get("city"), Card: r.PostForm.Get("pay_method") == "card"}
	if req.Governorate == "" {
		writeQuote(w, quoteResponse{Deliverable: true})
		return
	}
	if v := r.PostForm.Get("variant"); v != "" {
		id, idErr := ulid.Parse(v)
		qty, qtyErr := strconv.Atoi(r.PostForm.Get("qty"))
		if idErr != nil || qtyErr != nil || qty < 1 || qty > 20 {
			writeQuote(w, quoteResponse{Deliverable: true})
			return
		}
		req.Lines = []OrderRequestLine{{VariantID: id, Qty: qty}}
	} else {
		view, err := s.carts.Cart(ctx, s.cartToken(r))
		if err != nil {
			s.unavailable(w, r, err)
			return
		}
		for _, l := range view.Lines {
			req.Lines = append(req.Lines, OrderRequestLine{VariantID: l.VariantID, Qty: l.Qty})
		}
	}
	if len(req.Lines) == 0 {
		writeQuote(w, quoteResponse{Deliverable: true})
		return
	}

	q, err := s.orders.Quote(ctx, req)
	var invalid *OrderInvalidError
	switch {
	case errors.As(err, &invalid), errors.Is(err, ErrOrderUnavailable):
		writeQuote(w, quoteResponse{Deliverable: true}) // the form's own checks explain it on submit
		return
	case err != nil:
		s.unavailable(w, r, err)
		return
	case !q.Deliverable:
		writeQuote(w, quoteResponse{Text: p.l.T(MsgErrNotDelivered), Delivery: p.l.T(MsgErrNotDelivered)})
		return
	}
	writeQuote(w, quoteResponse{Text: quoteText(p.l, q), Deliverable: true,
		Delivery: deliveryLine(p.l, p.v.country, req.Governorate, q.Days), Shipping: shippingLine(p.l, q),
		Amounts: amounts(p.l, p.v.country, req, q)})
}

// amounts are the quote line by line, in the page's language.
func amounts(l Locale, country geo.Country, req QuoteRequest, q QuoteView) *quoteAmounts {
	a, _ := geo.Lookup(country, req.Governorate) // the quote accepted it
	out := &quoteAmounts{Subtotal: l.Price(q.Subtotal), ShipLabel: l.T(MsgShippingTo) + " " + a.Name(string(l)),
		Shipping: l.Price(q.Shipping), Total: l.Price(q.Total)}
	if q.Shipping.IsZero() {
		out.Shipping = l.T(MsgShippingFree)
	}
	if !q.CODFee.IsZero() {
		out.CODFee = l.Price(q.CODFee)
	}
	if !req.Card {
		out.CODNote = l.T(MsgCODCash)
		if out.CODFee != "" {
			out.CODNote += " · " + l.T(MsgFee) + " " + out.CODFee
		}
	}
	return out
}

// deliveryLine is where the order goes and, when the store says, how
// long it takes: "التوصيل إلى القاهرة خلال 2–3 أيام".
func deliveryLine(l Locale, country geo.Country, governorate string, days *DeliveryDays) string {
	a, _ := geo.Lookup(country, governorate) // the quote accepted it
	line := l.T(MsgDeliveryTo) + " " + a.Name(string(l))
	if days != nil {
		line += " " + l.DeliveryDays(days.Min, days.Max)
	}
	return line
}

// shippingLine is what shipping costs and from when it is free: "الشحن
// 50 ج.م · مجاني فوق 1,000 ج.م", or "الشحن مجاني".
func shippingLine(l Locale, q QuoteView) string {
	if q.Shipping.IsZero() {
		return l.T(MsgShippingFreeLine)
	}
	line := l.T(MsgShippingCost) + " " + l.Price(q.Shipping)
	if q.FreeOver != nil {
		line += " · " + l.T(MsgFreeOver) + " " + l.Price(*q.FreeOver)
	}
	return line
}

// quoteText is the quote in one line: shipping, the COD fee when there
// is one, and the total.
func quoteText(l Locale, q QuoteView) string {
	ship := l.Price(q.Shipping)
	if q.Shipping.IsZero() {
		ship = l.T(MsgShippingFree)
	}
	parts := []string{l.T(MsgShipping) + ": " + ship}
	if !q.CODFee.IsZero() {
		parts = append(parts, l.T(MsgCODFee)+": "+l.Price(q.CODFee))
	}
	parts = append(parts, l.T(MsgTotal)+": "+l.Price(q.Total))
	return strings.Join(parts, " · ")
}

func writeQuote(w http.ResponseWriter, q quoteResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(q) //nolint:errcheck // the shopper went away
}
