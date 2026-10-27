package app

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
)

// Settings are the store's checkout payment settings.
type Settings struct {
	DefaultMethod string // domain.DefaultCOD or domain.DefaultCard
	CardEnabled   bool   // a gateway is connected
}

// Settings reads the store's checkout payment settings.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	var out Settings
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		m, err := store.DefaultMethod(ctx, tx)
		if err != nil {
			return err
		}
		out.DefaultMethod = m
		_, err = store.ActiveConnection(ctx, tx)
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		out.CardEnabled = err == nil
		return err
	})
	return out, err
}

// UpdateSettings sets which method checkout starts on. Card may be
// chosen before a gateway is connected; it counts once one is.
func (s *Service) UpdateSettings(ctx context.Context, method string) (Settings, error) {
	if method != domain.DefaultCOD && method != domain.DefaultCard {
		return Settings{}, errs.Validation(errs.FieldError{Field: "default_method", Code: "invalid", Message: "default_method must be cod or card"})
	}
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if err := store.SaveDefaultMethod(ctx, tx, method); err != nil {
			return err
		}
		m, err := event.MetaFor(ctx, s.ids, domain.EventSettingsChanged, 1)
		if err != nil {
			return err
		}
		if err = s.events.PublishTx(ctx, tx, domain.SettingsChanged{Meta: m, DefaultMethod: method}); err != nil {
			return err
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "payment_settings.updated", TargetType: "payment_settings",
			Metadata: map[string]any{"default_method": method}})
	})
	if err != nil {
		return Settings{}, err
	}
	return s.Settings(ctx)
}

// CardDefault reports whether checkout starts on card: the merchant
// chose it and a gateway is connected.
func (s *Service) CardDefault(ctx context.Context) (bool, error) {
	st, err := s.Settings(ctx)
	return st.CardEnabled && st.DefaultMethod == domain.DefaultCard, err
}
