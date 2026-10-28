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
)

// maxToken bounds an SMS link token as received (ours are 22 characters).
const maxToken = 64

// LinkView is what the SMS link's page shows: the order (its code, its
// total, the customer's first name) and where its confirmation stands.
type LinkView struct {
	OrderCode string
	Total     money.Money
	FirstName string
	Status    domain.ConfirmationStatus
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
	return LinkView{OrderCode: o.Code(), Total: o.Total, FirstName: first, Status: c.Status}, nil
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
		if c.Status, err = s.AnswerTx(ctx, tx, c.OrderID, answer, domain.ActorSMSLink, s.now().UTC()); err != nil {
			return err
		}
		v, err = s.linkView(ctx, tx, c)
		return err
	})
	return v, err
}
