package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store/billingdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

func current(ctx context.Context) (ulid.ID, error) {
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return ulid.ID{}, fmt.Errorf("billing store: %w", err)
	}
	return id.ID, nil
}

// FeeRule is plan's fee in currency; ok is false when the plan charges
// none there.
func FeeRule(ctx context.Context, tx Tx, plan ulid.ID, currency string) (r domain.FeeRule, ok bool, err error) {
	row, err := billingdb.New(tx).GetFeeRule(ctx, billingdb.GetFeeRuleParams{PlanID: plan, Currency: currency})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return r, false, nil
	case err != nil:
		return r, false, fmt.Errorf("billing store: fee rule: %w", err)
	}
	return domain.FeeRule{RateBps: row.RateBps, MinMinor: row.MinMinor, MaxMinor: row.MaxMinor}, true, nil
}

// InsertSellFee records a delivered order's fee once; inserted is false
// when it was recorded before.
func InsertSellFee(ctx context.Context, tx Tx, f domain.SellFee) (inserted bool, err error) {
	store, err := current(ctx)
	if err != nil {
		return false, err
	}
	n, err := billingdb.New(tx).InsertSellFee(ctx, billingdb.InsertSellFeeParams{
		StoreID: store, OrderID: f.OrderID, Period: f.Period, PlanID: f.PlanID, BaseMinor: f.BaseMinor,
		RateBps: f.RateBps, FeeMinor: f.FeeMinor, Currency: f.Currency, DeliveredAt: f.DeliveredAt,
	})
	if err != nil {
		return false, fmt.Errorf("billing store: insert sell fee: %w", err)
	}
	return n == 1, nil
}

// SellFee is an order's fee; ok is false when it has none.
func SellFee(ctx context.Context, tx Tx, order ulid.ID) (f domain.SellFee, ok bool, err error) {
	store, err := current(ctx)
	if err != nil {
		return f, false, err
	}
	r, err := billingdb.New(tx).GetSellFee(ctx, billingdb.GetSellFeeParams{StoreID: store, OrderID: order})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return f, false, nil
	case err != nil:
		return f, false, fmt.Errorf("billing store: get sell fee: %w", err)
	}
	st, err := domain.ParseFeeStatus(r.Status)
	if err != nil {
		return f, false, err
	}
	return domain.SellFee{
		OrderID: r.OrderID, Period: r.Period, PlanID: r.PlanID, BaseMinor: r.BaseMinor, RateBps: r.RateBps, FeeMinor: r.FeeMinor,
		Currency: r.Currency, Status: st, DeliveredAt: r.DeliveredAt, ReturnedAt: at(r.ReturnedAt),
	}, true, nil
}

// SetSellFeeReturned records a return applied to an order's fee.
func SetSellFeeReturned(ctx context.Context, tx Tx, f domain.SellFee) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	returned := f.ReturnedAt.UTC()
	if err = billingdb.New(tx).SetSellFeeReturned(ctx, billingdb.SetSellFeeReturnedParams{
		StoreID: store, OrderID: f.OrderID, Status: string(f.Status), ReturnedAt: &returned,
	}); err != nil {
		return fmt.Errorf("billing store: sell fee returned: %w", err)
	}
	return nil
}
