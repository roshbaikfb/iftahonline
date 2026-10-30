// Package store adapts the sqlc queries in promotionsdb to domain types.
// Every function runs on a transaction opened by postgres.InTenantTx and
// takes the store ID from tenant.FromContext, never from its arguments
// (CLAUDE.md rule 5); RLS checks it a second time.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/promotions/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/promotions/internal/store/promotionsdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Tx is the tenant transaction the functions run on.
type Tx = promotionsdb.DBTX

// ErrCodeTaken means another of the store's coupons has the code.
var ErrCodeTaken = errors.New("promotions store: code taken")

func current(ctx context.Context) (ulid.ID, error) {
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return ulid.ID{}, fmt.Errorf("promotions store: %w", err)
	}
	return id.ID, nil
}

func int32p(p *int) *int32 {
	if p == nil {
		return nil
	}
	v := int32(*p) //nolint:gosec // bounded by domain.MaxUsesBound
	return &v
}

func intp(p *int32) *int {
	if p == nil {
		return nil
	}
	v := int(*p)
	return &v
}

func toDomain(r promotionsdb.Coupon) domain.Coupon {
	return domain.Coupon{ID: r.ID, Code: r.Code, Kind: domain.Kind(r.Kind), Percent: int(r.Percent), AmountMinor: r.AmountMinor,
		MinSubtotalMinor: r.MinSubtotalMinor, MaxUses: intp(r.MaxUses), MaxPerCustomer: intp(r.MaxPerCustomer),
		StartsAt: r.StartsAt, EndsAt: r.EndsAt, Active: r.Active, Uses: int(r.Uses), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

func one(r promotionsdb.Coupon, err error, what string) (domain.Coupon, error) {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.Coupon{}, domain.ErrNotFound
	case postgres.IsUniqueViolation(err, "coupons_store_code_key"):
		return domain.Coupon{}, ErrCodeTaken
	case err != nil:
		return domain.Coupon{}, fmt.Errorf("promotions store: %s: %w", what, err)
	}
	return toDomain(r), nil
}

// Insert adds a coupon.
func Insert(ctx context.Context, tx Tx, c domain.Coupon) (domain.Coupon, error) {
	return save(ctx, tx, c, false)
}

// Update saves a coupon's terms.
func Update(ctx context.Context, tx Tx, c domain.Coupon) (domain.Coupon, error) {
	return save(ctx, tx, c, true)
}

func save(ctx context.Context, tx Tx, c domain.Coupon, update bool) (domain.Coupon, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Coupon{}, err
	}
	p := promotionsdb.InsertCouponParams{
		StoreID: store, ID: c.ID, Code: c.Code, Kind: string(c.Kind), Percent: int32(c.Percent), AmountMinor: c.AmountMinor, //nolint:gosec // 0..100
		MinSubtotalMinor: c.MinSubtotalMinor, MaxUses: int32p(c.MaxUses), MaxPerCustomer: int32p(c.MaxPerCustomer),
		StartsAt: c.StartsAt, EndsAt: c.EndsAt, Active: c.Active,
	}
	q := promotionsdb.New(tx)
	if !update {
		r, iErr := q.InsertCoupon(ctx, p)
		return one(r, iErr, "insert")
	}
	r, err := q.UpdateCoupon(ctx, promotionsdb.UpdateCouponParams{
		StoreID: p.StoreID, ID: p.ID, Code: p.Code, Kind: p.Kind, Percent: p.Percent, AmountMinor: p.AmountMinor,
		MinSubtotalMinor: p.MinSubtotalMinor, MaxUses: p.MaxUses, MaxPerCustomer: p.MaxPerCustomer, StartsAt: p.StartsAt,
		EndsAt: p.EndsAt, Active: p.Active,
	})
	return one(r, err, "update")
}

// Get returns a coupon.
func Get(ctx context.Context, tx Tx, id ulid.ID) (domain.Coupon, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Coupon{}, err
	}
	r, err := promotionsdb.New(tx).GetCoupon(ctx, promotionsdb.GetCouponParams{StoreID: store, ID: id})
	return one(r, err, "get")
}

// List returns the store's coupons, newest first.
func List(ctx context.Context, tx Tx, before ulid.ID, limit int) ([]domain.Coupon, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := promotionsdb.New(tx).ListCoupons(ctx, promotionsdb.ListCouponsParams{StoreID: store, Before: before, Lim: int32(limit)}) //nolint:gosec // bounded by the caller
	if err != nil {
		return nil, fmt.Errorf("promotions store: list: %w", err)
	}
	out := make([]domain.Coupon, len(rows))
	for i, r := range rows {
		out[i] = toDomain(r)
	}
	return out, nil
}

// ByCode returns the coupon of a normalized code.
func ByCode(ctx context.Context, tx Tx, code string) (domain.Coupon, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Coupon{}, err
	}
	r, err := promotionsdb.New(tx).CouponByCode(ctx, promotionsdb.CouponByCodeParams{StoreID: store, Code: code})
	return one(r, err, "by code")
}

// LockCustomer holds a customer's uses of a coupon until tx ends, so the
// customer's concurrent orders count them one at a time.
func LockCustomer(ctx context.Context, tx Tx, coupon, customer ulid.ID) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = promotionsdb.New(tx).LockCustomerUses(ctx, promotionsdb.LockCustomerUsesParams{StoreID: store, CouponID: coupon, CustomerID: customer}); err != nil {
		return fmt.Errorf("promotions store: lock customer: %w", err)
	}
	return nil
}

// DeleteUnused deletes a coupon no order ever held; it reports whether
// it did.
func DeleteUnused(ctx context.Context, tx Tx, id ulid.ID) (bool, error) {
	store, err := current(ctx)
	if err != nil {
		return false, err
	}
	n, err := promotionsdb.New(tx).DeleteUnusedCoupon(ctx, promotionsdb.DeleteUnusedCouponParams{StoreID: store, ID: id})
	if err != nil {
		return false, fmt.Errorf("promotions store: delete: %w", err)
	}
	return n == 1, nil
}

// CustomerUses counts a customer's held uses of a coupon.
func CustomerUses(ctx context.Context, tx Tx, coupon, customer ulid.ID) (int, error) {
	store, err := current(ctx)
	if err != nil {
		return 0, err
	}
	n, err := promotionsdb.New(tx).CustomerUses(ctx, promotionsdb.CustomerUsesParams{StoreID: store, CouponID: coupon, CustomerID: customer})
	if err != nil {
		return 0, fmt.Errorf("promotions store: customer uses: %w", err)
	}
	return int(n), nil
}

// ErrNoUseLeft means the coupon had no use left (or was turned off) by
// the time the order took one.
var ErrNoUseLeft = errors.New("promotions store: no use left")

// Redeem records that an order holds a coupon and takes a use; it
// reports false (taking nothing) when the order holds one already, and
// ErrNoUseLeft when none is left.
func Redeem(ctx context.Context, tx Tx, order, customer ulid.ID, d domain.Discount) (bool, error) {
	store, err := current(ctx)
	if err != nil {
		return false, err
	}
	q := promotionsdb.New(tx)
	n, err := q.InsertRedemption(ctx, promotionsdb.InsertRedemptionParams{StoreID: store, OrderID: order, CouponID: d.CouponID,
		CustomerID: customer, DiscountMinor: d.Amount.Amount(), FreeShipping: d.FreeShipping})
	if err != nil {
		return false, fmt.Errorf("promotions store: redeem: %w", err)
	}
	if n == 0 {
		return false, nil
	}
	_, err = q.ConsumeUse(ctx, promotionsdb.ConsumeUseParams{StoreID: store, ID: d.CouponID})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, ErrNoUseLeft
	case err != nil:
		return false, fmt.Errorf("promotions store: take use: %w", err)
	}
	return true, nil
}

// Release gives an order's use back, once; it reports whether it did.
func Release(ctx context.Context, tx Tx, order ulid.ID) (bool, error) {
	store, err := current(ctx)
	if err != nil {
		return false, err
	}
	q := promotionsdb.New(tx)
	coupon, err := q.ReleaseRedemption(ctx, promotionsdb.ReleaseRedemptionParams{StoreID: store, OrderID: order})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("promotions store: release: %w", err)
	}
	if err = q.UncountUse(ctx, promotionsdb.UncountUseParams{StoreID: store, ID: coupon}); err != nil {
		return false, fmt.Errorf("promotions store: uncount use: %w", err)
	}
	return true, nil
}
