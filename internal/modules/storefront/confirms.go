package storefront

import (
	"context"
	"errors"

	"github.com/roshbaikfb/iftahonline/internal/modules/messaging"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/web"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// shopConfirms adapts messaging's SMS link side to the shop's port: a
// confirmation's status becomes what the page may say.
type shopConfirms struct {
	links messaging.ConfirmLinks
}

var _ web.Confirmations = shopConfirms{}

func (c shopConfirms) Confirmation(ctx context.Context, token string) (web.ConfirmView, error) {
	return toConfirmView(c.links.LinkConfirmation(ctx, token))
}

func (c shopConfirms) AnswerConfirmation(ctx context.Context, token string, confirm bool) (web.ConfirmView, error) {
	answer := messaging.Cancel
	if confirm {
		answer = messaging.Confirm
	}
	return toConfirmView(c.links.AnswerLink(ctx, token, answer))
}

func (c shopConfirms) WhatsAppLink(ctx context.Context, orderRef, locale string) (string, error) {
	id, err := ulid.Parse(orderRef)
	if err != nil {
		return "", nil // no order has that reference
	}
	return c.links.WhatsAppLink(ctx, id, locale)
}

func toConfirmView(v messaging.LinkView, err error) (web.ConfirmView, error) {
	if errors.Is(err, messaging.ErrConfirmationNotFound) {
		return web.ConfirmView{}, web.ErrConfirmationNotFound
	}
	if err != nil {
		return web.ConfirmView{}, err
	}
	out := web.ConfirmView{Code: v.OrderCode, Total: v.Total, FirstName: v.FirstName, State: web.ConfirmClosed}
	switch v.Status {
	case messaging.ConfirmationPending, messaging.ConfirmationUnreached:
		// An unreached customer who found the link may still answer.
		out.State = web.ConfirmWaiting
	case messaging.ConfirmationConfirmed:
		out.State = web.ConfirmDone
		if v.AwaitingCall {
			out.State = web.ConfirmAwaitingCall
		}
	case messaging.ConfirmationCancelled:
		out.State = web.ConfirmCancelled
	}
	return out, nil
}
