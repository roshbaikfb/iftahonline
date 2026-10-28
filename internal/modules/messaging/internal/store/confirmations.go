package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/store/messagingdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// ErrConfirmationNotFound is a missing confirmation.
var ErrConfirmationNotFound = errors.New("messaging store: no confirmation")

// Settings returns the store's settings (the defaults until it chose).
func Settings(ctx context.Context, tx Tx) (domain.Settings, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Settings{}, err
	}
	row, err := messagingdb.New(tx).GetSettings(ctx, store)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.DefaultSettings(), nil
	case err != nil:
		return domain.Settings{}, fmt.Errorf("messaging store: settings: %w", err)
	}
	return domain.Settings{ConfirmCOD: row.ConfirmCod, AutoCancelHours: int(row.AutoCancelHours)}, nil
}

// SaveSettings saves the store's settings.
func SaveSettings(ctx context.Context, tx Tx, s domain.Settings) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if _, err = messagingdb.New(tx).SaveSettings(ctx, messagingdb.SaveSettingsParams{
		StoreID: store, ConfirmCod: s.ConfirmCOD, AutoCancelHours: int32(s.AutoCancelHours), //nolint:gosec // 1..72
	}); err != nil {
		return fmt.Errorf("messaging store: save settings: %w", err)
	}
	return nil
}

// ErrCodeTaken means the WhatsApp code is another order's (draw again).
var ErrCodeTaken = errors.New("messaging store: WhatsApp code taken")

// InsertConfirmation opens an order's confirmation; it reports false
// when the order has one already. waCode may be empty (no WhatsApp
// number to confirm on).
func InsertConfirmation(ctx context.Context, tx Tx, c domain.Confirmation, tokenHash []byte, waCode string) (bool, error) {
	store, err := current(ctx)
	if err != nil {
		return false, err
	}
	var code *string
	if waCode != "" {
		code = &waCode
	}
	n, err := messagingdb.New(tx).InsertConfirmation(ctx, messagingdb.InsertConfirmationParams{
		StoreID: store, OrderID: c.OrderID, Status: string(c.Status), TokenHash: tokenHash, WaCode: code, ExpiresAt: c.ExpiresAt,
	})
	if postgres.IsUniqueViolation(err, "order_confirmations_wa_code") {
		return false, ErrCodeTaken
	}
	if err != nil {
		return false, fmt.Errorf("messaging store: insert confirmation: %w", err)
	}
	return n == 1, nil
}

// ByCode finds the store of a WhatsApp code, across stores (the
// platform's one number receives every store's texts). tx has no tenant.
func ByCode(ctx context.Context, tx Tx, code string) (tenant.StoreID, bool, error) {
	id, err := messagingdb.New(tx).ConfirmationStoreByCode(ctx, code)
	if err != nil {
		return tenant.StoreID{}, false, fmt.Errorf("messaging store: store by code: %w", err)
	}
	return tenant.StoreID{ID: id}, !id.IsZero(), nil
}

// ConfirmationByCode returns the confirmation of a WhatsApp code in the
// store in ctx.
func ConfirmationByCode(ctx context.Context, tx Tx, code string) (domain.Confirmation, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Confirmation{}, err
	}
	row, err := messagingdb.New(tx).ConfirmationByCode(ctx, messagingdb.ConfirmationByCodeParams{StoreID: store, WaCode: &code})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.Confirmation{}, ErrConfirmationNotFound
	case err != nil:
		return domain.Confirmation{}, fmt.Errorf("messaging store: confirmation by code: %w", err)
	}
	return toConfirmation(row), nil
}

func toConfirmation(row messagingdb.OrderConfirmation) domain.Confirmation {
	c := domain.Confirmation{OrderID: row.OrderID, MessageID: row.MessageID, Status: domain.ConfirmationStatus(row.Status),
		ExpiresAt: row.ExpiresAt, AnsweredAt: row.AnsweredAt, AnsweredBy: row.AnsweredBy, CreatedAt: row.CreatedAt}
	if row.WaCode != nil {
		c.WACode = *row.WaCode
	}
	return c
}

// Confirmation returns an order's confirmation; lock holds it until tx
// ends.
func Confirmation(ctx context.Context, tx Tx, order ulid.ID, lock bool) (domain.Confirmation, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Confirmation{}, err
	}
	q := messagingdb.New(tx)
	var row messagingdb.OrderConfirmation
	if lock {
		row, err = q.LockConfirmation(ctx, messagingdb.LockConfirmationParams{StoreID: store, OrderID: order})
	} else {
		row, err = q.GetConfirmation(ctx, messagingdb.GetConfirmationParams{StoreID: store, OrderID: order})
	}
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.Confirmation{}, ErrConfirmationNotFound
	case err != nil:
		return domain.Confirmation{}, fmt.Errorf("messaging store: confirmation: %w", err)
	}
	return toConfirmation(row), nil
}

// UpdateConfirmation saves a confirmation's message, status and answer.
func UpdateConfirmation(ctx context.Context, tx Tx, c domain.Confirmation) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = messagingdb.New(tx).UpdateConfirmation(ctx, messagingdb.UpdateConfirmationParams{
		StoreID: store, OrderID: c.OrderID, MessageID: c.MessageID, Status: string(c.Status), AnsweredAt: c.AnsweredAt, AnsweredBy: c.AnsweredBy,
	}); err != nil {
		return fmt.Errorf("messaging store: update confirmation: %w", err)
	}
	return nil
}

// notificationRetention is how long notices are kept.
const notificationRetention = 90 * 24 * time.Hour

// InsertNotification adds a notice once per (kind, dedup key), purging a
// few expired ones; it reports whether this one was new.
func InsertNotification(ctx context.Context, tx Tx, n domain.Notification) (bool, error) {
	store, err := current(ctx)
	if err != nil {
		return false, err
	}
	data, err := json.Marshal(n.Data)
	if err != nil {
		return false, fmt.Errorf("messaging store: notice data: %w", err)
	}
	q := messagingdb.New(tx)
	if err = q.PurgeNotifications(ctx, messagingdb.PurgeNotificationsParams{StoreID: store, Before: n.ID.Time().Add(-notificationRetention)}); err != nil {
		return false, fmt.Errorf("messaging store: purge notices: %w", err)
	}
	rows, err := q.InsertNotification(ctx, messagingdb.InsertNotificationParams{StoreID: store, ID: n.ID, Kind: string(n.Kind), DedupKey: n.DedupKey, Data: data})
	if err != nil {
		return false, fmt.Errorf("messaging store: insert notice: %w", err)
	}
	return rows == 1, nil
}

// Notification returns a notice.
func Notification(ctx context.Context, tx Tx, id ulid.ID) (domain.Notification, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Notification{}, err
	}
	row, err := messagingdb.New(tx).GetNotification(ctx, messagingdb.GetNotificationParams{StoreID: store, ID: id})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.Notification{}, domain.ErrNotFound
	case err != nil:
		return domain.Notification{}, fmt.Errorf("messaging store: notice: %w", err)
	}
	return toNotification(row)
}

// Notifications lists notices before (newest first); unread only when
// asked.
func Notifications(ctx context.Context, tx Tx, before ulid.ID, unreadOnly bool, limit int) ([]domain.Notification, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := messagingdb.New(tx).ListNotifications(ctx, messagingdb.ListNotificationsParams{
		StoreID: store, BeforeID: before, UnreadOnly: unreadOnly, Lim: int32(limit), //nolint:gosec // bounded by the caller
	})
	if err != nil {
		return nil, fmt.Errorf("messaging store: notices: %w", err)
	}
	out := make([]domain.Notification, len(rows))
	for i, r := range rows {
		if out[i], err = toNotification(r); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// UnreadNotifications counts the store's unread notices.
func UnreadNotifications(ctx context.Context, tx Tx) (int64, error) {
	store, err := current(ctx)
	if err != nil {
		return 0, err
	}
	n, err := messagingdb.New(tx).CountUnreadNotifications(ctx, store)
	if err != nil {
		return 0, fmt.Errorf("messaging store: unread notices: %w", err)
	}
	return n, nil
}

// MarkNotificationsRead marks the store's notices read.
func MarkNotificationsRead(ctx context.Context, tx Tx, ids []ulid.ID) (int64, error) {
	store, err := current(ctx)
	if err != nil {
		return 0, err
	}
	n, err := messagingdb.New(tx).MarkNotificationsRead(ctx, messagingdb.MarkNotificationsReadParams{StoreID: store, Ids: ids})
	if err != nil {
		return 0, fmt.Errorf("messaging store: mark read: %w", err)
	}
	return n, nil
}

func toNotification(r messagingdb.MerchantNotification) (domain.Notification, error) {
	n := domain.Notification{ID: r.ID, Kind: domain.NoticeKind(r.Kind), DedupKey: r.DedupKey, ReadAt: r.ReadAt, CreatedAt: r.CreatedAt}
	if err := json.Unmarshal(r.Data, &n.Data); err != nil {
		return domain.Notification{}, fmt.Errorf("messaging store: notice data: %w", err)
	}
	return n, nil
}

// ConfirmationByToken returns the confirmation an SMS link's token opens.
func ConfirmationByToken(ctx context.Context, tx Tx, tokenHash []byte) (domain.Confirmation, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Confirmation{}, err
	}
	row, err := messagingdb.New(tx).ConfirmationByToken(ctx, messagingdb.ConfirmationByTokenParams{StoreID: store, TokenHash: tokenHash})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.Confirmation{}, ErrConfirmationNotFound
	case err != nil:
		return domain.Confirmation{}, fmt.Errorf("messaging store: confirmation by token: %w", err)
	}
	return toConfirmation(row), nil
}
