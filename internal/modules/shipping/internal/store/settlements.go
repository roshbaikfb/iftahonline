package store

import (
	"context"
	"fmt"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/store/shippingdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// SettlementsAdded counts what an import did.
type SettlementsAdded struct {
	Imported   int // new lines
	Duplicates int // lines already imported (same tracking number and reference)
	Matched    int // new lines matched to a shipment
}

// InsertSettlements stores a statement's lines for a connection in one
// round trip, matching each to its shipment by tracking number.
func InsertSettlements(ctx context.Context, tx Tx, ids *ulid.Generator, conn domain.Connection, cur money.Currency, source string,
	lines []domain.SettlementLine) (SettlementsAdded, error) {
	store, err := current(ctx)
	if err != nil {
		return SettlementsAdded{}, err
	}
	params := make([]shippingdb.InsertSettlementParams, len(lines))
	for i, l := range lines {
		params[i] = shippingdb.InsertSettlementParams{ID: ids.New(), StoreID: store, ConnectionID: conn.ID, Carrier: conn.Carrier,
			TrackingNumber: l.TrackingNumber, Currency: string(cur), AmountMinor: l.Amount.Amount(), SettledAt: l.SettledAt,
			Reference: l.Reference, Source: source}
	}
	var out SettlementsAdded
	var batchErr error
	shippingdb.New(tx).InsertSettlement(ctx, params).Query(func(_ int, rows []shippingdb.InsertSettlementRow, err error) {
		switch {
		case batchErr != nil:
		case err != nil:
			batchErr = err
		case len(rows) == 0:
			out.Duplicates++
		default:
			out.Imported++
			if rows[0].ShipmentID != nil {
				out.Matched++
			}
		}
	})
	if batchErr != nil {
		return SettlementsAdded{}, fmt.Errorf("shipping store: insert settlements: %w", batchErr)
	}
	return out, nil
}

// CODSummary is the per-carrier COD report of parcels delivered in
// [from, to), held amounts aged as of asOf.
func CODSummary(ctx context.Context, tx Tx, from, to, asOf time.Time) ([]domain.CODCarrier, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := shippingdb.New(tx).CODSummary(ctx, shippingdb.CODSummaryParams{StoreID: store, FromAt: from, ToAt: to, AsOf: asOf})
	if err != nil {
		return nil, fmt.Errorf("shipping store: cod summary: %w", err)
	}
	out := make([]domain.CODCarrier, 0, len(rows))
	for _, r := range rows {
		cur := money.Currency(r.Currency)
		c := domain.CODCarrier{ConnectionID: r.ConnectionID, Carrier: r.Carrier, Currency: cur, Delivered: int(r.Delivered), OverSettled: int(r.OverSettled)}
		for _, f := range []struct {
			dst *money.Money
			v   int64
		}{{&c.Expected, r.ExpectedMinor}, {&c.Settled, r.SettledMinor}, {&c.Held, r.HeldMinor}, {&c.Held0to7, r.Held07},
			{&c.Held8to14, r.Held814}, {&c.Held15Plus, r.Held15Plus}} {
			if *f.dst, err = money.New(f.v, cur); err != nil {
				return nil, fmt.Errorf("shipping store: %w", err)
			}
		}
		out = append(out, c)
	}
	return out, nil
}

// Unmatched counts the store's settlement lines that match no shipment.
func Unmatched(ctx context.Context, tx Tx) (lines int, amountMinor int64, err error) {
	store, err := current(ctx)
	if err != nil {
		return 0, 0, err
	}
	r, err := shippingdb.New(tx).UnmatchedSettlements(ctx, store)
	if err != nil {
		return 0, 0, fmt.Errorf("shipping store: unmatched settlements: %w", err)
	}
	return int(r.Lines), r.AmountMinor, nil
}

// Held lists delivered COD parcels not fully settled, oldest first,
// after the (delivered at, shipment) position.
func Held(ctx context.Context, tx Tx, after *domain.Cursor, limit int) ([]domain.HeldParcel, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	p := shippingdb.HeldShipmentsParams{StoreID: store, AfterAt: time.Unix(0, 0).UTC(), Lim: int32(limit)} //nolint:gosec // bounded by the caller
	if after != nil {
		p.AfterAt, p.AfterID = after.At, after.ID
	}
	rows, err := shippingdb.New(tx).HeldShipments(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("shipping store: held shipments: %w", err)
	}
	out := make([]domain.HeldParcel, len(rows))
	for i, r := range rows {
		cur := money.Currency(r.Currency)
		h := domain.HeldParcel{ShipmentID: r.ID, OrderID: r.OrderID, Carrier: r.Carrier, DeliveredAt: r.StatusAt}
		if r.TrackingNumber != nil {
			h.TrackingNumber = *r.TrackingNumber
		}
		if h.Expected, err = money.New(r.CodMinor, cur); err != nil {
			return nil, fmt.Errorf("shipping store: %w", err)
		}
		if h.Settled, err = money.New(r.SettledMinor, cur); err != nil {
			return nil, fmt.Errorf("shipping store: %w", err)
		}
		out[i] = h
	}
	return out, nil
}
