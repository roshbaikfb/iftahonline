package main

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// shippingOrders adapts orders.Fulfilment to what shipments need
// (shipping cannot import orders, which prices deliveries through it).
// The fulfilment is set after the orders module is built, since orders
// needs shipping's quoter first.
type shippingOrders struct {
	f orders.Fulfilment
}

var _ shipping.Orders = (*shippingOrders)(nil)

func (a *shippingOrders) Order(ctx context.Context, id ulid.ID) (shipping.OrderInfo, error) {
	o, err := a.f.Get(ctx, id)
	if errors.Is(err, orders.ErrOrderNotFound) {
		return shipping.OrderInfo{}, shipping.ErrOrderUnknown
	}
	if err != nil {
		return shipping.OrderInfo{}, err
	}
	items := make([]string, 0, len(o.Lines))
	for _, l := range o.Lines {
		t := l.Title
		if l.VariantTitle != "" {
			t += " (" + l.VariantTitle + ")"
		}
		items = append(items, t+" × "+strconv.Itoa(l.Qty))
	}
	c := o.Contact
	cod := o.Total
	if o.PaymentMethod == orders.PaymentCard { // paid online: the courier collects nothing
		if cod, err = money.Zero(o.Total.Currency()); err != nil {
			return shipping.OrderInfo{}, err
		}
	}
	return shipping.OrderInfo{
		ID: o.ID, Status: string(o.Status), Name: c.Name, Phone: c.Phone.Unmasked(), Governorate: c.Governorate,
		City: c.City, Address: c.Address, Notes: c.Notes, COD: cod, Items: o.ItemCount,
		Description: strings.Join(items, "، "),
	}, nil
}

func (a *shippingOrders) MoveTx(ctx context.Context, tx pgx.Tx, id ulid.ID, to, actor, note string) (bool, error) {
	moved, err := a.f.TransitionSystemTx(ctx, tx, id, orders.Status(to), actor, note)
	if errors.Is(err, orders.ErrInvalidTransition) {
		return false, shipping.ErrOrderMoveRefused
	}
	return moved, err
}
