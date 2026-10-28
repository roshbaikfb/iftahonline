package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/text"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Error codes the log keeps when a message fails on our side of a
// channel.
const (
	codeUndeliverable = "undeliverable"
	codeRejected      = "rejected"
	codeAuth          = "auth"
	codeUnavailable   = "unavailable"
)

// slot is the channel a message goes out on next: WhatsApp unless it
// already fell back to SMS (or WhatsApp is not configured).
func (s *Service) slot(m domain.Message) (channel.Channel, domain.Channel) {
	if !m.Fallback && s.channels.WhatsApp != nil {
		return s.channels.WhatsApp, domain.WhatsApp
	}
	if s.channels.SMS != nil {
		return s.channels.SMS, domain.SMS
	}
	return nil, ""
}

// SendMessage runs SendMessageArgs. A transient failure is returned for
// River to retry, except on the job's last try (lastTry), when the
// message fails. Only a queued message is sent, so a retried job after a
// recorded send does nothing; a send whose answer was lost is sent again
// (channels take no idempotency key; a second reply changes nothing).
func (s *Service) SendMessage(ctx context.Context, id ulid.ID, lastTry bool) error {
	m, err := s.Get(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil || m.Status != domain.Queued {
		return err
	}
	ch, via := s.slot(m)
	if ch == nil {
		return s.fail(ctx, id, errNoChannel)
	}
	msg, err := s.build(ctx, m, via)
	if err != nil {
		return err
	}
	sent, err := ch.Send(ctx, msg)
	switch {
	case err == nil:
		return s.markSent(ctx, id, ch.Name(), via, sent)
	case errors.Is(err, channel.ErrUndeliverable) && via == domain.WhatsApp && s.channels.SMS != nil:
		return s.fallback(ctx, id, codeUndeliverable)
	case errors.Is(err, channel.ErrUndeliverable):
		return s.fail(ctx, id, codeUndeliverable)
	case errors.Is(err, channel.ErrRejected):
		s.log.WarnContext(ctx, "messaging: channel rejected a message", "channel", ch.Name(), "message_id", id.String(), "err", text.Redact(err.Error()))
		return s.fail(ctx, id, codeRejected)
	case errors.Is(err, channel.ErrAuth):
		s.log.ErrorContext(ctx, "messaging: channel refused the platform's credentials", "channel", ch.Name())
		return s.fail(ctx, id, codeAuth)
	case lastTry:
		return s.fail(ctx, id, codeUnavailable)
	}
	return fmt.Errorf("messaging: send via %s: %w", ch.Name(), err)
}

// build opens m's content and writes the message for its channel.
func (s *Service) build(ctx context.Context, m domain.Message, via domain.Channel) (channel.Message, error) {
	storeID, err := tenant.FromContext(ctx)
	if err != nil {
		return channel.Message{}, err
	}
	raw, err := s.keyring.Open(m.Content, aad(storeID, m.ID))
	if err != nil {
		return channel.Message{}, fmt.Errorf("messaging: open content: %w", err)
	}
	var c domain.Content
	if err = json.Unmarshal(raw, &c); err != nil {
		return channel.Message{}, fmt.Errorf("messaging: content: %w", err)
	}
	to, err := phone.ParseE164(c.To)
	if err != nil {
		return channel.Message{}, fmt.Errorf("messaging: recipient: %w", err)
	}
	ref := domain.Ref{Store: storeID, Message: m.ID}
	out := channel.Message{To: to, Ref: ref.String()}
	if via == domain.WhatsApp {
		tpl, ok := domain.WhatsAppTemplate(m.Kind)
		if !ok {
			return channel.Message{}, fmt.Errorf("%w: no template for %q", domain.ErrInvalid, m.Kind)
		}
		out.Template, out.Language, out.Params = tpl.Name, m.Locale, domain.TemplateParams(m.Kind, c.Vars)
		for _, r := range tpl.Buttons {
			out.Buttons = append(out.Buttons, channel.Button{Payload: s.buttons.Payload(ref, r)})
		}
		return out, nil
	}
	body, err := s.smsBody(ctx, m.Kind, m.Locale)
	if err != nil {
		return channel.Message{}, err
	}
	out.Text = domain.RenderSMS(body, c.Vars)
	return out, nil
}

// update locks message id and applies change to it in one transaction;
// change reports whether it changed anything.
func (s *Service) update(ctx context.Context, id ulid.ID, change func(tx pgx.Tx, m *domain.Message) (bool, error)) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		m, err := store.Get(ctx, tx, id, true)
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		changed, err := change(tx, &m)
		if err != nil || !changed {
			return err
		}
		return store.Update(ctx, tx, m)
	})
}

func (s *Service) markSent(ctx context.Context, id ulid.ID, provider string, via domain.Channel, sent channel.Sent) error {
	return s.update(ctx, id, func(_ pgx.Tx, m *domain.Message) (bool, error) {
		if m.Status != domain.Queued {
			return false, nil
		}
		m.Status, m.Channel, m.Provider, m.ProviderID, m.ErrorCode = domain.Sent, via, provider, sent.ProviderID, ""
		return true, nil
	})
}

// fail ends a message that no channel delivered, and gives its quota
// unit back.
func (s *Service) fail(ctx context.Context, id ulid.ID, code string) error {
	return s.update(ctx, id, func(tx pgx.Tx, m *domain.Message) (bool, error) {
		return s.failTx(ctx, tx, m, code)
	})
}

func (s *Service) failTx(ctx context.Context, tx pgx.Tx, m *domain.Message, code string) (bool, error) {
	if !m.Status.Advances(domain.Failed) {
		return false, nil
	}
	m.Status, m.ErrorCode = domain.Failed, text.Clip(code, 40)
	if m.QuotaPeriod != nil {
		if _, err := s.quota.ReleaseTx(ctx, tx, entitlements.Messages, *m.QuotaPeriod, 1); err != nil {
			return false, err
		}
	}
	return true, nil
}

// fallback sends m again by SMS, once, after WhatsApp could not reach the
// customer. It costs no second quota unit.
func (s *Service) fallback(ctx context.Context, id ulid.ID, code string) error {
	return s.update(ctx, id, func(tx pgx.Tx, m *domain.Message) (bool, error) {
		return s.fallbackTx(ctx, tx, m, code)
	})
}

func (s *Service) fallbackTx(ctx context.Context, tx pgx.Tx, m *domain.Message, code string) (bool, error) {
	if m.Fallback || s.channels.SMS == nil || (m.Status != domain.Queued && m.Status != domain.Sent) {
		return false, nil
	}
	m.Fallback, m.Status, m.Channel, m.Provider, m.ProviderID, m.ErrorCode = true, domain.Queued, "", "", "", text.Clip(code, 40)
	return true, s.enqueueSendTx(ctx, tx, m.ID, true)
}
