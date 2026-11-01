// Package store adapts the sqlc queries in riskdb to domain types. Every
// function runs on a transaction opened by postgres.InTenantTx and takes
// the store ID from tenant.FromContext, never from its arguments
// (CLAUDE.md rule 5); RLS checks it a second time. Signals is the one
// read across stores, through app.risk_signals (ADR-0065).
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/store/riskdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Tx is the tenant transaction the functions run on.
type Tx = riskdb.DBTX

// MaxKeys is the most phone keys one Signals call reads (the function's
// own bound).
const MaxKeys = 100

func current(ctx context.Context) (ulid.ID, error) {
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return ulid.ID{}, fmt.Errorf("risk store: %w", err)
	}
	return id.ID, nil
}

// Order is what one order teaches: its phone, and its outcome once it
// has one.
type Order struct {
	ID        ulid.ID
	PhoneKey  []byte
	PlacedAt  time.Time
	Outcome   domain.Outcome // "" for none yet
	OutcomeAt time.Time
}

// Upsert writes the orders in one statement; an order whose row already
// says the same is left alone.
func Upsert(ctx context.Context, tx Tx, orders []Order) error {
	if len(orders) == 0 {
		return nil
	}
	store, err := current(ctx)
	if err != nil {
		return err
	}
	p := riskdb.UpsertRiskOrdersParams{StoreID: store}
	for _, o := range orders {
		p.OrderIds = append(p.OrderIds, o.ID)
		p.PhoneKeys = append(p.PhoneKeys, o.PhoneKey)
		p.PlacedAts = append(p.PlacedAts, o.PlacedAt)
		p.Outcomes = append(p.Outcomes, string(o.Outcome))
		p.OutcomeAts = append(p.OutcomeAts, o.OutcomeAt)
	}
	if err = riskdb.New(tx).UpsertRiskOrders(ctx, p); err != nil {
		return fmt.Errorf("risk store: upsert: %w", err)
	}
	return nil
}

// Keys returns the phone keys of those of ids the store has learnt.
func Keys(ctx context.Context, tx Tx, ids []ulid.ID) (map[ulid.ID][]byte, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := riskdb.New(tx).RiskOrderKeys(ctx, riskdb.RiskOrderKeysParams{OrderIds: ids, StoreID: store})
	if err != nil {
		return nil, fmt.Errorf("risk store: keys: %w", err)
	}
	out := make(map[ulid.ID][]byte, len(rows))
	for _, r := range rows {
		out[r.OrderID] = r.PhoneKey
	}
	return out, nil
}

// Signals returns the platform's recent outcomes of each key (at most
// MaxKeys keys), keyed by string(key). It reveals no store or order.
func Signals(ctx context.Context, tx Tx, keys [][]byte) (map[string][]domain.Signal, error) {
	if len(keys) > MaxKeys {
		return nil, fmt.Errorf("risk store: %d keys, at most %d", len(keys), MaxKeys)
	}
	if _, err := current(ctx); err != nil {
		return nil, err
	}
	rows, err := riskdb.New(tx).RiskSignals(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("risk store: signals: %w", err)
	}
	out := make(map[string][]domain.Signal, len(keys))
	for _, r := range rows {
		k := string(r.PhoneKey)
		out[k] = append(out[k], domain.Signal{Outcome: domain.Outcome(r.Outcome), At: r.OutcomeAt, Store: int(r.StoreNo)})
	}
	return out, nil
}

// History returns a phone's orders in the store.
func History(ctx context.Context, tx Tx, key []byte) (domain.History, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.History{}, err
	}
	r, err := riskdb.New(tx).RiskHistory(ctx, riskdb.RiskHistoryParams{StoreID: store, PhoneKey: key})
	if err != nil {
		return domain.History{}, fmt.Errorf("risk store: history: %w", err)
	}
	h := domain.History{Orders: int(r.Orders), Outcomes: map[domain.Outcome]int{
		domain.Delivered: int(r.Delivered), domain.Returned: int(r.Returned), domain.Refused: int(r.Refused),
		domain.NoAnswer: int(r.NoAnswer), domain.Unconfirmed: int(r.Unconfirmed),
	}}
	if r.LastAt.After(time.Unix(0, 0)) {
		h.LastAt = r.LastAt.UTC()
	}
	return h, nil
}

// QueueBackfill marks the store's past as queued; false when it already
// was.
func QueueBackfill(ctx context.Context, tx Tx) (bool, error) {
	store, err := current(ctx)
	if err != nil {
		return false, err
	}
	if _, err = riskdb.New(tx).QueueRiskBackfill(ctx, store); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("risk store: queue backfill: %w", err)
	}
	return true, nil
}

// Purge deletes up to n rows past the horizon, of every store; it returns
// how many. It needs no tenant: the function decides what is old.
func Purge(ctx context.Context, tx Tx, n int) (int, error) {
	purged, err := riskdb.New(tx).PurgeRiskOrders(ctx, int32(min(n, 10000))) //nolint:gosec // bounded
	if err != nil {
		return 0, fmt.Errorf("risk store: purge: %w", err)
	}
	return int(purged), nil
}

// Rules returns the store's risk rules; a store without them has both
// off.
func Rules(ctx context.Context, tx Tx) (domain.Rules, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Rules{}, err
	}
	rows, err := riskdb.New(tx).RiskSettings(ctx, store)
	if err != nil {
		return domain.Rules{}, fmt.Errorf("risk store: rules: %w", err)
	}
	if len(rows) == 0 {
		return domain.Rules{}.Normalized(), nil
	}
	return domain.Rules{HoldFrom: domain.Band(rows[0].HoldFrom), VerifyFrom: domain.Band(rows[0].VerifyFrom),
		PrepayFrom: domain.Band(rows[0].PrepayFrom)}, nil
}

// SaveRules writes the store's risk rules.
func SaveRules(ctx context.Context, tx Tx, r domain.Rules) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = riskdb.New(tx).SaveRiskSettings(ctx, riskdb.SaveRiskSettingsParams{
		StoreID: store, HoldFrom: string(r.HoldFrom), VerifyFrom: string(r.VerifyFrom), PrepayFrom: string(r.PrepayFrom),
	}); err != nil {
		return fmt.Errorf("risk store: save rules: %w", err)
	}
	return nil
}
