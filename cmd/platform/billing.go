package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/mailer"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/text"
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

// ownerNotices are where a billing notice goes besides email: the
// owners' verified phones, on WhatsApp (ADR-0054).
type ownerNotices struct {
	phones   func(context.Context) ([]string, error)
	whatsapp messaging.MerchantSender
	log      *slog.Logger
}

// billingNotifier emails a billing notice to the store's owners, in the
// store's language, with a link to its billing page, then sends it on
// WhatsApp to their verified phones. WhatsApp is best effort: a failure
// is logged, not retried, so a retry never emails twice.
func billingNotifier(ten *tenancy.Module, owners func(context.Context) ([]string, error), m mailer.Mailer, dashboardURL string, wa ownerNotices) billing.Notifier {
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
		wa.send(ctx, n, string(st.Locale), st.Name)
		return nil
	}
}

// merchantNotice is a billing notice's WhatsApp template and values.
func merchantNotice(n billing.Notice, store string) (messaging.MerchantKind, []string) {
	switch n.Kind {
	case billing.NoticePaymentFailed:
		closes := billing.NoticeDate(n.GraceUntil)
		if closes == "" {
			closes = "-"
		}
		return messaging.MerchantPaymentFailed, []string{store, n.Invoice, n.Amount(), closes}
	case billing.NoticeSuspended:
		return messaging.MerchantSuspended, []string{store}
	default:
		return messaging.MerchantPaid, []string{store, n.Invoice, n.Amount()}
	}
}

func (o ownerNotices) send(ctx context.Context, n billing.Notice, locale, store string) {
	if o.whatsapp == nil || o.phones == nil {
		return
	}
	phones, err := o.phones(ctx)
	if err != nil {
		o.log.WarnContext(ctx, "billing: owners' phones", "err", err)
		return
	}
	kind, values := merchantNotice(n, store)
	for _, p := range phones {
		if err = o.whatsapp.SendMerchant(ctx, p, kind, locale, values); err != nil {
			o.log.WarnContext(ctx, "billing: WhatsApp notice not sent", "kind", string(kind), "err", text.Redact(err.Error()))
		}
	}
}

// sellFeesHandler follows each order's status to its fee: subscribed as
// billing.SellFeesSubscriber to orders.order.status_changed.
func sellFeesHandler(fees billing.Fees) eventbus.Handler {
	return eventbus.Typed(func(ctx context.Context, ev orders.OrderStatusChanged) error { return fees.OrderMoved(ctx, ev.OrderID) })
}
