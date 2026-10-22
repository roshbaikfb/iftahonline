package storefront

import (
	"context"
	"errors"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/customers"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/web"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// shopOrders adapts the orders module to the shop's order port: it maps
// the orders module's errors to what a shopper may be told. A blocked
// customer and a reused idempotency key both read as a refusal; field
// errors name only the form's fields.
type shopOrders struct {
	shop orders.Shop
}

var _ web.Orders = shopOrders{}

func (o shopOrders) PlaceOrder(ctx context.Context, req web.OrderRequest) (string, error) {
	in := orders.NewOrder{IdempotencyKey: req.Key, Source: orders.SourceStorefront, Name: req.Name, Phone: req.Phone,
		Governorate: req.Governorate, City: req.City, Address: req.Address, Notes: req.Notes}
	for _, l := range req.Lines {
		in.Lines = append(in.Lines, orders.LineInput{VariantID: l.VariantID, Qty: l.Qty})
	}
	p, err := o.shop.PlaceOrder(ctx, in)
	if err == nil {
		return p.Order.ID.String(), nil
	}
	var e *errs.Error
	switch {
	case errors.Is(err, customers.ErrCustomerBlocked), errors.Is(err, orders.ErrIdempotencyKeyReused):
		return "", web.ErrOrderRefused
	case errors.Is(err, catalog.ErrInsufficientStock):
		return "", web.ErrOrderOutOfStock
	case errors.Is(err, catalog.ErrVariantNotSellable):
		return "", web.ErrOrderUnavailable
	case errors.As(err, &e) && e.Kind == errs.KindValidation:
		inv := &web.OrderInvalidError{}
		for _, f := range e.Fields {
			name := f.Field
			if len(name) >= 5 && name[:5] == "lines" {
				name = "lines"
			}
			inv.Fields = append(inv.Fields, name)
		}
		return "", inv
	}
	return "", err
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
	s := web.OrderSummary{Ref: ref, Code: ord.Code(), Total: ord.Total}
	for _, l := range ord.Lines {
		title := l.Title
		if l.VariantTitle != "" {
			title += " (" + l.VariantTitle + ")"
		}
		s.Lines = append(s.Lines, web.OrderLine{Title: title, Qty: l.Qty, Total: l.Total})
	}
	return s, nil
}
