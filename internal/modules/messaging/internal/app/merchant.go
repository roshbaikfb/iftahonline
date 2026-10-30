package app

import (
	"context"
	"fmt"

	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
)

// SendMerchant sends a merchant notice on WhatsApp from the platform's
// number, now (ADR-0054): to is a member's verified phone, values the
// template's body values in order. Without WhatsApp configured it sends
// nothing. Not logged with the store's messages, not counted in its
// quota; the caller's job retries an error.
func (s *Service) SendMerchant(ctx context.Context, to string, k domain.MerchantKind, locale string, values []string) error {
	name, n, ok := domain.MerchantTemplate(k)
	if !ok || len(values) != n || !domain.ValidLocale(locale) {
		return fmt.Errorf("%w: merchant notice %q with %d values in %q", domain.ErrInvalid, k, len(values), locale)
	}
	num, err := phone.ParseE164(to)
	if err != nil {
		return fmt.Errorf("%w: merchant phone: %w", domain.ErrInvalid, err)
	}
	if s.channels.WhatsApp == nil {
		return nil
	}
	_, err = s.channels.WhatsApp.Send(ctx, channel.Message{To: num, Template: name, Language: locale,
		Params: domain.MerchantParams(values), Ref: "mn_" + s.ids.New().String()})
	if err != nil {
		return fmt.Errorf("messaging: merchant notice: %w", err)
	}
	return nil
}
