// Package store adapts the sqlc queries in webhooksdb to domain types.
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

	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/internal/store/webhooksdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Tx is the tenant transaction the functions run on.
type Tx = webhooksdb.DBTX

func current(ctx context.Context) (ulid.ID, error) {
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return ulid.ID{}, fmt.Errorf("webhooks store: %w", err)
	}
	return id.ID, nil
}

// Accept stores a receipt and its body. It reports false, storing
// nothing, when the provider event was accepted before.
func Accept(ctx context.Context, tx Tx, r domain.Receipt, contentType string, body []byte) (bool, error) {
	store, err := current(ctx)
	if err != nil {
		return false, err
	}
	q := webhooksdb.New(tx)
	n, err := q.InsertReceipt(ctx, webhooksdb.InsertReceiptParams{
		ID: r.ID, StoreID: store, Provider: r.Provider, ConnectionID: r.ConnectionID, EventID: r.EventID, ReceivedAt: r.ReceivedAt,
	})
	if err != nil {
		return false, fmt.Errorf("webhooks store: insert receipt: %w", err)
	}
	if n == 0 {
		return false, nil
	}
	if err = q.InsertBody(ctx, webhooksdb.InsertBodyParams{
		StoreID: store, ReceivedAt: r.ReceivedAt, ReceiptID: r.ID, ContentType: contentType, Body: body,
	}); err != nil {
		return false, fmt.Errorf("webhooks store: insert body: %w", err)
	}
	return true, nil
}

// Purge deletes a few of the store's receipts received before t.
func Purge(ctx context.Context, tx Tx, before time.Time) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = webhooksdb.New(tx).PurgeReceipts(ctx, webhooksdb.PurgeReceiptsParams{StoreID: store, Before: before}); err != nil {
		return fmt.Errorf("webhooks store: purge: %w", err)
	}
	return nil
}

// Get returns a receipt with its body, or domain.ErrNotFound. The body
// is gone (nil) once its partition expired.
func Get(ctx context.Context, tx Tx, id ulid.ID) (r domain.Receipt, contentType string, body []byte, err error) {
	store, err := current(ctx)
	if err != nil {
		return r, "", nil, err
	}
	q := webhooksdb.New(tx)
	row, err := q.GetReceipt(ctx, webhooksdb.GetReceiptParams{StoreID: store, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return r, "", nil, domain.ErrNotFound
	}
	if err != nil {
		return r, "", nil, fmt.Errorf("webhooks store: get receipt: %w", err)
	}
	r = domain.Receipt{ID: row.ID, Provider: row.Provider, ConnectionID: row.ConnectionID, EventID: row.EventID,
		ReceivedAt: row.ReceivedAt, ProcessedAt: row.ProcessedAt, Outcome: row.Outcome}
	b, err := q.GetBody(ctx, webhooksdb.GetBodyParams{StoreID: store, ReceivedAt: row.ReceivedAt, ReceiptID: row.ID})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return r, "", nil, nil
	case err != nil:
		return r, "", nil, fmt.Errorf("webhooks store: get body: %w", err)
	}
	return r, b.ContentType, b.Body, nil
}

// MarkProcessed records a receipt's outcome once; it reports false when
// the receipt was already processed (or does not exist).
func MarkProcessed(ctx context.Context, tx Tx, id ulid.ID, outcome string) (bool, error) {
	store, err := current(ctx)
	if err != nil {
		return false, err
	}
	n, err := webhooksdb.New(tx).MarkProcessed(ctx, webhooksdb.MarkProcessedParams{StoreID: store, ID: id, Outcome: outcome})
	if err != nil {
		return false, fmt.Errorf("webhooks store: mark processed: %w", err)
	}
	return n > 0, nil
}
