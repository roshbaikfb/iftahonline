package main

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping"
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
	return orderInfo(o, err)
}

// OrderTx reads the order in the caller's transaction.
func (a *shippingOrders) OrderTx(ctx context.Context, tx pgx.Tx, id ulid.ID) (shipping.OrderInfo, error) {
	o, err := a.f.GetTx(ctx, tx, id)
	return orderInfo(o, err)
}

func orderInfo(o orders.Order, err error) (shipping.OrderInfo, error) {
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
	// The courier collects what was not paid online: nothing for a card
	// order, the rest after a prepaid delivery (ADR-0067).
	cod, err := o.OnDelivery()
	if err != nil {
		return shipping.OrderInfo{}, err
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
