package main

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// billingOrders adapts orders to what pay-as-you-sell fees need
// (ADR-0048): billing never imports orders, which import entitlements,
// which import billing.
func billingOrders(f orders.Fulfilment) billing.Orders {
	return func(ctx context.Context, tx pgx.Tx, id ulid.ID) (billing.Order, error) {
		o, err := f.GetTx(ctx, tx, id)
		if errors.Is(err, orders.ErrOrderNotFound) {
			return billing.Order{Status: billing.OrderOther}, nil
		}
		if err != nil {
			return billing.Order{}, err
		}
		out := billing.Order{StatusAt: o.StatusChangedAt}
		switch o.Status {
		case orders.StatusDelivered:
			out.Status = billing.OrderDelivered
		case orders.StatusReturned:
			out.Status = billing.OrderReturned
		default:
			return billing.Order{Status: billing.OrderOther}, nil
		}
		if out.Base, err = o.Subtotal.Sub(o.Discount); err != nil {
			return billing.Order{}, err
		}
		return out, nil
	}
}

// sellFeesHandler follows each order's status to its fee: subscribed as
// billing.SellFeesSubscriber to orders.order.status_changed.
func sellFeesHandler(fees billing.Fees) eventbus.Handler {
	return eventbus.Typed(func(ctx context.Context, ev orders.OrderStatusChanged) error { return fees.OrderMoved(ctx, ev.OrderID) })
}
