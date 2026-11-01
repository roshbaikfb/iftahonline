package app

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// maxToken bounds an SMS link token as received (ours are 22 characters).
const maxToken = 64

// LinkView is what the SMS link's page shows: the order (its code, its
// total, the customer's first name) and where its confirmation stands.
// AwaitingCall: the customer said yes, but the order waits for the
// store's call (the risk rules, ADR-0070), so it is not confirmed yet.
//
// A prepaid delivery's link pays it (ADR-0070): Deposit is the amount,
// DepositDue whether it is still to pay (the order pending), DepositPaid
// whether it was, and OnDelivery what the courier collects.
type LinkView struct {
	OrderID      ulid.ID
	OrderCode    string
	Total        money.Money
	FirstName    string
	Status       domain.ConfirmationStatus
	AwaitingCall bool
	Deposit      money.Money
	DepositDue   bool
	DepositPaid  bool
	OnDelivery   money.Money
}

// byToken finds the confirmation of the store in ctx an SMS link's
// token opens.
func byToken(ctx context.Context, tx pgx.Tx, token string) (domain.Confirmation, error) {
	if token == "" || len(token) > maxToken {
		return domain.Confirmation{}, ErrConfirmationNotFound
	}
	c, err := store.ConfirmationByToken(ctx, tx, domain.HashToken(token))
	if errors.Is(err, store.ErrConfirmationNotFound) {
		return domain.Confirmation{}, ErrConfirmationNotFound
	}
	return c, err
}

func (s *Service) linkView(ctx context.Context, tx pgx.Tx, c domain.Confirmation) (LinkView, error) {
	o, err := s.conf.Orders.GetTx(ctx, tx, c.OrderID)
	if errors.Is(err, orders.ErrOrderNotFound) {
		return LinkView{}, ErrConfirmationNotFound
	}
	if err != nil {
		return LinkView{}, err
	}
	first, _, _ := strings.Cut(strings.TrimSpace(o.Contact.Name), " ")
	v := LinkView{OrderID: o.ID, OrderCode: o.Code(), Total: o.Total, FirstName: first, Status: c.Status,
		AwaitingCall: awaitingCall(c.Status, o)}
	if o.RiskAction == orders.RiskPrepay && o.DepositStatus != "" {
		v.Deposit = o.Deposit
		v.DepositDue = o.DepositStatus == orders.PaymentAwaiting && o.Status == orders.StatusPending
		v.DepositPaid = o.DepositStatus == orders.PaymentPaid
		if v.OnDelivery, err = o.OnDelivery(); err != nil {
			return LinkView{}, err
		}
	}
	return v, nil
}

// awaitingCall reports whether a yes left the order pending: the store
// calls before it is confirmed, and the customer must not be told it is.
func awaitingCall(settled domain.ConfirmationStatus, o orders.Order) bool {
	return settled == domain.Confirmed && o.Status == orders.StatusPending
}

// LinkConfirmation is the SMS link's page: the order its token names,
// in the store in ctx. A token of another store, or none, is
// ErrConfirmationNotFound.
func (s *Service) LinkConfirmation(ctx context.Context, token string) (LinkView, error) {
	if !s.conf.ready() {
		return LinkView{}, errNoOrders
	}
	var v LinkView
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		c, err := byToken(ctx, tx, token)
		if err != nil {
			return err
		}
		v, err = s.linkView(ctx, tx, c)
		return err
	})
	return v, err
}

// AnswerLink is the customer's answer on the SMS link's page (actor
// customer:sms). Answering again shows how it settled.
func (s *Service) AnswerLink(ctx context.Context, token string, answer domain.Reply) (LinkView, error) {
	if !s.conf.ready() {
		return LinkView{}, errNoOrders
	}
	var v LinkView
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		c, err := byToken(ctx, tx, token)
		if err != nil {
			return err
		}
		if c.Status, _, err = s.AnswerTx(ctx, tx, c.OrderID, answer, domain.ActorSMSLink, s.now().UTC()); err != nil {
			return err
		}
		v, err = s.linkView(ctx, tx, c)
		return err
	})
	return v, err
}

// ValidPayLink reports whether token is the SMS link of orderID, in the
// store in ctx: the link sent to the customer's phone, which pays a
// prepaid delivery (payments' DepositLinkCheck, ADR-0070).
func (s *Service) ValidPayLink(ctx context.Context, orderID ulid.ID, token string) (bool, error) {
	var c domain.Confirmation
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (err error) {
		c, err = byToken(ctx, tx, token)
		return err
	})
	if errors.Is(err, ErrConfirmationNotFound) {
		return false, nil
	}
	return err == nil && c.OrderID == orderID, err
}

// WhatsAppLink is the thank-you page's "confirm on WhatsApp" link for an
// order of the store in ctx, in locale: the platform's number with the
// ready text carrying the order's code. It is "" when the order waits
// for no answer, has no code, the platform has no number, or the store
// does not confirm orders: then a confirmation the risk rules asked for
// is not offered either, so the page says nothing of them (ADR-0070).
func (s *Service) WhatsAppLink(ctx context.Context, orderID ulid.ID, locale string) (string, error) {
	if s.conf.Number == "" {
		return "", nil
	}
	var c domain.Confirmation
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		set, err := store.Settings(ctx, tx)
		if err != nil || !set.ConfirmCOD {
			return err
		}
		c, err = store.Confirmation(ctx, tx, orderID, false)
		return err
	})
	if errors.Is(err, store.ErrConfirmationNotFound) {
		return "", nil
	}
	if err != nil || c.WACode == "" || (c.Status != domain.Pending && c.Status != domain.Unreached) {
		return "", err
	}
	return domain.WhatsAppLink(s.conf.Number, domain.CustomerConfirmText(locale, c.WACode)), nil
}
