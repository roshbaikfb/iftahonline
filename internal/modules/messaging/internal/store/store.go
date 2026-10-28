// Package store adapts the sqlc queries in messagingdb to domain types.
// Every function runs on a transaction opened by postgres.InTenantTx and
// takes the store ID from tenant.FromContext, never from its arguments
// (CLAUDE.md rule 5); RLS checks it a second time.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/store/messagingdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Tx is the tenant transaction the functions run on.
type Tx = messagingdb.DBTX

func current(ctx context.Context) (ulid.ID, error) {
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return ulid.ID{}, fmt.Errorf("messaging store: %w", err)
	}
	return id.ID, nil
}

// Insert adds a message to the log.
func Insert(ctx context.Context, tx Tx, m domain.Message) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = messagingdb.New(tx).InsertMessage(ctx, messagingdb.InsertMessageParams{
		StoreID: store, CreatedAt: m.CreatedAt, ID: m.ID, Kind: string(m.Kind), Locale: m.Locale, OrderID: m.OrderID,
		Status: string(m.Status), ErrorCode: m.ErrorCode, ToMasked: m.ToMasked, Content: m.Content, QuotaPeriod: m.QuotaPeriod,
	}); err != nil {
		return fmt.Errorf("messaging store: insert: %w", err)
	}
	return nil
}

// Get returns a message; lock holds its row until tx ends.
func Get(ctx context.Context, tx Tx, id ulid.ID, lock bool) (domain.Message, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Message{}, err
	}
	q := messagingdb.New(tx)
	var row messagingdb.Message
	if lock {
		row, err = q.LockMessage(ctx, messagingdb.LockMessageParams{StoreID: store, CreatedAt: domain.CreatedAtOf(id), ID: id})
	} else {
		row, err = q.GetMessage(ctx, messagingdb.GetMessageParams{StoreID: store, CreatedAt: domain.CreatedAtOf(id), ID: id})
	}
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.Message{}, domain.ErrNotFound
	case err != nil:
		return domain.Message{}, fmt.Errorf("messaging store: get: %w", err)
	}
	return toDomain(row), nil
}

// Update saves a message's delivery state: status, channel, provider and
// its id, fallback and error code.
func Update(ctx context.Context, tx Tx, m domain.Message) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = messagingdb.New(tx).UpdateMessage(ctx, messagingdb.UpdateMessageParams{
		StoreID: store, CreatedAt: m.CreatedAt, ID: m.ID, Status: string(m.Status), Channel: string(m.Channel), Provider: m.Provider,
		ProviderMessageID: m.ProviderID, Fallback: m.Fallback, ErrorCode: m.ErrorCode,
	}); err != nil {
		return fmt.Errorf("messaging store: update: %w", err)
	}
	return nil
}

// RecordReply keeps the customer's first answer; it reports whether this
// one was it.
func RecordReply(ctx context.Context, tx Tx, id ulid.ID, reply domain.Reply, at time.Time) (bool, error) {
	store, err := current(ctx)
	if err != nil {
		return false, err
	}
	_, err = messagingdb.New(tx).RecordReply(ctx, messagingdb.RecordReplyParams{
		StoreID: store, CreatedAt: domain.CreatedAtOf(id), ID: id, Reply: string(reply), RepliedAt: &at,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("messaging store: reply: %w", err)
	}
	return true, nil
}

func toDomain(r messagingdb.Message) domain.Message {
	return domain.Message{
		ID: r.ID, Kind: domain.Kind(r.Kind), Locale: r.Locale, OrderID: r.OrderID, Status: domain.Status(r.Status),
		Channel: domain.Channel(r.Channel), Provider: r.Provider, ToMasked: r.ToMasked, ProviderID: r.ProviderMessageID,
		Fallback: r.Fallback, ErrorCode: r.ErrorCode, QuotaPeriod: r.QuotaPeriod, Content: r.Content,
		Reply: domain.Reply(r.Reply), RepliedAt: r.RepliedAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}
