package app

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var (
	errBadHours = errs.Validation(errs.FieldError{Field: "auto_cancel_hours", Code: "range", Message: "auto_cancel_hours must be 1 to 72"})
	errBadIDs   = errs.Validation(errs.FieldError{Field: "ids", Code: "range", Message: "1 to 100 notification IDs"})
)

// Settings returns the store's messaging settings.
func (s *Service) Settings(ctx context.Context) (domain.Settings, error) {
	var out domain.Settings
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (err error) {
		out, err = store.Settings(ctx, tx)
		return err
	})
	return out, err
}

// SaveSettings saves the store's messaging settings. A new auto-cancel
// time applies to orders placed from now on.
func (s *Service) SaveSettings(ctx context.Context, set domain.Settings) (domain.Settings, error) {
	if !set.Valid() {
		return domain.Settings{}, errBadHours
	}
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if err := store.SaveSettings(ctx, tx, set); err != nil {
			return err
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "messaging_settings.updated", TargetType: "messaging_settings",
			Metadata: map[string]any{"confirm_cod": set.ConfirmCOD, "auto_cancel_hours": set.AutoCancelHours}})
	})
	if err != nil {
		return domain.Settings{}, err
	}
	return set, nil
}

// Confirmation returns an order's confirmation.
func (s *Service) Confirmation(ctx context.Context, order ulid.ID) (domain.Confirmation, error) {
	var c domain.Confirmation
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (err error) {
		c, err = store.Confirmation(ctx, tx, order, false)
		return err
	})
	if errors.Is(err, store.ErrConfirmationNotFound) {
		return domain.Confirmation{}, ErrConfirmationNotFound
	}
	return c, err
}

// NoticePage is one page of notices, with the store's unread count.
type NoticePage struct {
	Items  []domain.Notification
	Next   *domain.Notification
	Unread int64
}

// Notifications lists the store's notices, newest first, after a notice
// (exclusive).
func (s *Service) Notifications(ctx context.Context, after *ulid.ID, unreadOnly bool, limit int) (NoticePage, error) {
	if limit == 0 {
		limit = DefaultPage
	}
	if limit < 1 || limit > MaxPage {
		return NoticePage{}, errPageSize
	}
	before := ulid.ID{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	if after != nil {
		before = *after
	}
	var p NoticePage
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (err error) {
		if p.Items, err = store.Notifications(ctx, tx, before, unreadOnly, limit+1); err != nil {
			return err
		}
		p.Unread, err = store.UnreadNotifications(ctx, tx)
		return err
	})
	if err != nil {
		return NoticePage{}, err
	}
	if len(p.Items) > limit {
		p.Items = p.Items[:limit]
		p.Next = &p.Items[limit-1]
	}
	return p, nil
}

// MarkNotificationsRead marks the store's notices read; it returns how
// many were unread. IDs of other stores' notices change nothing.
func (s *Service) MarkNotificationsRead(ctx context.Context, ids []ulid.ID) (int64, error) {
	if len(ids) == 0 || len(ids) > 100 {
		return 0, errBadIDs
	}
	var n int64
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (err error) {
		n, err = store.MarkNotificationsRead(ctx, tx, ids)
		return err
	})
	return n, err
}
