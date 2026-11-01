package storefront

import (
	"context"
	"errors"
	"strings"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/customers"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/web"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// shopOrders adapts the orders module to the shop's order port: it maps
// the orders module's errors to what a shopper may be told. A blocked
// customer and a reused idempotency key both read as a refusal; field
// errors name only the form's fields.
type shopOrders struct {
	shop    orders.Shop
	parcels shipping.Tracker // nil: tracking shows no parcel
}

var _ web.Orders = shopOrders{}

func (o shopOrders) PlaceOrder(ctx context.Context, req web.OrderRequest) (web.Placed, error) {
	in := orders.NewOrder{IdempotencyKey: req.Key, Source: orders.SourceStorefront, PaymentMethod: method(req.Card), Name: req.Name,
		Phone: req.Phone, Governorate: req.Governorate, City: req.City, Address: req.Address, Notes: req.Notes, CouponCode: req.Coupon}
	v := req.Visit
	in.Attribution = orders.Attribution{Source: v.UTMSource, Medium: v.UTMMedium, Campaign: v.UTMCampaign, Content: v.UTMContent, Term: v.UTMTerm}
	in.Signals = orders.Signals{FBP: v.FBP, FBC: v.FBC, TTP: v.TTP, TTCLID: v.TTCLID, ScCID: v.ScCID, GCLID: v.GCLID, IP: v.IP, UserAgent: v.UserAgent}
	for _, l := range req.Lines {
		in.Lines = append(in.Lines, orders.LineInput{VariantID: l.VariantID, Qty: l.Qty})
	}
	p, err := o.shop.PlaceOrder(ctx, in)
	if err == nil {
		// Only a card order goes on to pay: a prepaid delivery's link goes
		// to the customer's phone (ADR-0070).
		_, due := p.Order.OnlineDue()
		return web.Placed{Ref: p.Order.ID.String(), PayOnline: due && p.Order.PaymentMethod == orders.PaymentCard}, nil
	}
	var e *errs.Error
	switch {
	case errors.Is(err, customers.ErrCustomerBlocked), errors.Is(err, orders.ErrIdempotencyKeyReused):
		return web.Placed{}, web.ErrOrderRefused
	case errors.Is(err, catalog.ErrInsufficientStock):
		return web.Placed{}, web.ErrOrderOutOfStock
	case errors.Is(err, catalog.ErrVariantNotSellable):
		return web.Placed{}, web.ErrOrderUnavailable
	case errors.As(err, &e) && e.Kind == errs.KindValidation:
		return web.Placed{}, invalidOrder(e)
	}
	return web.Placed{}, err
}

// invalidOrder names the form fields of a validation error.
func invalidOrder(e *errs.Error) *web.OrderInvalidError {
	inv := &web.OrderInvalidError{}
	for _, f := range e.Fields {
		name := f.Field
		if len(name) >= 5 && name[:5] == "lines" {
			name = "lines"
		}
		if name == "governorate" && f.Code == "not_delivered" {
			inv.NotDelivered = true
		}
		if name == "coupon_code" {
			inv.Coupon = f.Code
		}
		inv.Fields = append(inv.Fields, name)
	}
	return inv
}

func method(card bool) orders.PaymentMethod {
	if card {
		return orders.PaymentCard
	}
	return orders.PaymentCOD
}

// shopPayments adapts the payments module to the shop's payment port:
// the storefront reads whether the store takes cards, and sends card
// shoppers to the API host to pay (ADR-0030). It holds no gateway keys.
type shopPayments struct {
	cards  payments.CardCheck
	origin string
}

var _ web.Payments = shopPayments{}

func (p shopPayments) CardEnabled(ctx context.Context) (bool, error) { return p.cards.CardEnabled(ctx) }

func (p shopPayments) CardDefault(ctx context.Context) (bool, error) { return p.cards.CardDefault(ctx) }

func (p shopPayments) PayURL(store, orderRef, back string) string {
	s, err := tenant.ParseStoreID(store)
	if err != nil {
		return ""
	}
	id, err := ulid.Parse(orderRef)
	if err != nil {
		return ""
	}
	return payments.StartURL(p.origin, s, id, back)
}

func (p shopPayments) DepositURL(store, orderRef, back, token string) string {
	s, err := tenant.ParseStoreID(store)
	if err != nil {
		return ""
	}
	id, err := ulid.Parse(orderRef)
	if err != nil {
		return ""
	}
	return payments.DepositURL(p.origin, s, id, back, token)
}

func (o shopOrders) Quote(ctx context.Context, req web.QuoteRequest) (web.QuoteView, error) {
	in := orders.QuoteInput{Governorate: req.Governorate, City: req.City, PaymentMethod: method(req.Card), CouponCode: req.Coupon}
	for _, l := range req.Lines {
		in.Lines = append(in.Lines, orders.LineInput{VariantID: l.VariantID, Qty: l.Qty})
	}
	q, err := o.shop.Quote(ctx, in)
	var e *errs.Error
	switch {
	case err == nil:
		v := web.QuoteView{Deliverable: q.Deliverable, FreeShipping: q.FreeShipping, Subtotal: q.Subtotal, Shipping: q.Shipping,
			CODFee: q.CODFee, Total: q.Total, Discount: q.Discount, Coupon: q.CouponCode}
		if q.FreeOverMinor != nil {
			if m, mErr := money.New(*q.FreeOverMinor, q.Subtotal.Currency()); mErr == nil {
				v.FreeOver = &m
			}
		}
		if d := q.Delivery; d != nil {
			v.Days = &web.DeliveryDays{Min: d.Min, Max: d.Max}
		}
		return v, nil
	case errors.Is(err, catalog.ErrVariantNotSellable):
		return web.QuoteView{}, web.ErrOrderUnavailable
	case errors.As(err, &e) && e.Kind == errs.KindValidation:
		return web.QuoteView{}, invalidOrder(e)
	}
	return web.QuoteView{}, err
}

func (o shopOrders) Track(ctx context.Context, code, phone string) (web.Tracking, error) {
	ord, err := o.shop.Track(ctx, code, phone)
	if errors.Is(err, orders.ErrOrderNotFound) {
		return web.Tracking{}, web.ErrOrderNotFound
	}
	if err != nil {
		return web.Tracking{}, err
	}
	t := web.Tracking{Code: ord.Code(), Status: string(ord.Status), PlacedAt: ord.CreatedAt, Card: ord.PaymentMethod == orders.PaymentCard,
		PaymentStatus: string(ord.PaymentStatus), Items: ord.ItemCount, Total: ord.Total}
	if o.parcels != nil {
		p, ok, pErr := o.parcels.Parcel(ctx, ord.ID)
		if pErr != nil {
			return web.Tracking{}, pErr
		}
		if ok {
			t.Parcel = &web.Parcel{Carrier: p.Carrier, Number: p.TrackingNumber, Status: string(p.Status)}
		}
	}
	return t, nil
}

func (o shopOrders) Bestsellers(ctx context.Context, limit int) ([]ulid.ID, error) {
	return o.shop.Bestsellers(ctx, min(limit, orders.MaxBestsellers))
}

func (o shopOrders) OrderSummary(ctx context.Context, ref string) (web.OrderSummary, error) {
	id, err := ulid.Parse(ref)
	if err != nil {
		return web.OrderSummary{}, web.ErrOrderNotFound
	}
	ord, err := o.shop.Get(ctx, id)
	if errors.Is(err, orders.ErrOrderNotFound) {
		return web.OrderSummary{}, web.ErrOrderNotFound
	}
	if err != nil {
		return web.OrderSummary{}, err
	}
	s := web.OrderSummary{Ref: ref, Code: ord.Code(), Card: ord.PaymentMethod == orders.PaymentCard, PaymentStatus: string(ord.PaymentStatus),
		Cancelled: ord.Status == orders.StatusCancelled, Subtotal: ord.Subtotal, Shipping: ord.Shipping, CODFee: ord.CODFee,
		Discount: ord.Discount, CouponCode: ord.CouponCode, Total: ord.Total, Governorate: ord.Contact.Governorate, City: ord.Contact.City}
	if f := strings.Fields(ord.Contact.Name); len(f) > 0 {
		s.FirstName = f[0]
	}
	in := orders.QuoteInput{Governorate: ord.Contact.Governorate, City: ord.Contact.City, PaymentMethod: ord.PaymentMethod}
	for _, l := range ord.Lines {
		title := l.Title
		if l.VariantTitle != "" {
			title += " (" + l.VariantTitle + ")"
		}
		s.Lines = append(s.Lines, web.OrderLine{VariantID: l.VariantID, Title: title, Qty: l.Qty, Total: l.Total})
		in.Lines = append(in.Lines, orders.LineInput{VariantID: l.VariantID, Qty: l.Qty})
	}
	// How long delivery takes is today's word for the zone: the order
	// keeps the price it was quoted, not the days. When the order can no
	// longer be quoted (a product off sale), the page says no days.
	if q, qErr := o.shop.Quote(ctx, in); qErr == nil && q.Delivery != nil {
		s.Days = &web.DeliveryDays{Min: q.Delivery.Min, Max: q.Delivery.Max}
	}
	return s, nil
}
