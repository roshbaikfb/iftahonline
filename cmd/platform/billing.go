package main

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/mailer"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
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

// billingStores adapts tenancy to what invoices need: a store's name and
// currency.
func billingStores(ten *tenancy.Module) billing.Stores {
	stores := ten.Service()
	return func(ctx context.Context) (billing.StoreInfo, error) {
		st, err := stores.GetStore(ctx)
		if err != nil {
			return billing.StoreInfo{}, err
		}
		return billing.StoreInfo{Name: st.Name, Currency: string(st.Currency)}, nil
	}
}

// billingHolds adapts tenancy's billing hold (ADR-0051).
func billingHolds(ten *tenancy.Module) billing.Holds {
	stores := ten.Service()
	return func(ctx context.Context, tx pgx.Tx, hold bool) error { return stores.SetBillingHoldTx(ctx, tx, hold) }
}

// billingNotifier emails a billing notice to the store's owners, in the
// store's language, with a link to its billing page.
func billingNotifier(ten *tenancy.Module, owners func(context.Context) ([]string, error), m mailer.Mailer, dashboardURL string) billing.Notifier {
	stores := ten.Service()
	return func(ctx context.Context, n billing.Notice) error {
		id, err := tenant.FromContext(ctx)
		if err != nil {
			return err
		}
		st, err := stores.GetStore(ctx)
		if err != nil {
			return err
		}
		to, err := owners(ctx)
		if err != nil {
			return err
		}
		subject, text := n.Email(string(st.Locale), st.Name, dashboardURL+"/stores/"+id.String()+"/billing")
		for _, addr := range to {
			if err = m.Send(ctx, mailer.Message{To: addr, Subject: subject, Text: text}); err != nil {
				return err
			}
		}
		return nil
	}
}

// sellFeesHandler follows each order's status to its fee: subscribed as
// billing.SellFeesSubscriber to orders.order.status_changed.
func sellFeesHandler(fees billing.Fees) eventbus.Handler {
	return eventbus.Typed(func(ctx context.Context, ev orders.OrderStatusChanged) error { return fees.OrderMoved(ctx, ev.OrderID) })
}
