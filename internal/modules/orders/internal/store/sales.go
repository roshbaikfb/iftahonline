package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/store/ordersdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Sale is an order's units of one product.
type Sale struct {
	ProductID ulid.ID
	Units     int
}

// SaleCounted reports whether an order's units are in the bestsellers.
func SaleCounted(ctx context.Context, tx Tx, orderID ulid.ID) (bool, error) {
	store, err := current(ctx)
	if err != nil {
		return false, err
	}
	ok, err := ordersdb.New(tx).IsSaleCounted(ctx, ordersdb.IsSaleCountedParams{StoreID: store, OrderID: orderID})
	if err != nil {
		return false, fmt.Errorf("orders store: sale counted: %w", err)
	}
	return ok, nil
}

// CountSale adds (or, with add false, takes back) an order's units on its
// day and marks it counted (or not).
func CountSale(ctx context.Context, tx Tx, orderID ulid.ID, day time.Time, sales []Sale, add bool) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	q := ordersdb.New(tx)
	ids, units := make([]ulid.ID, len(sales)), make([]int32, len(sales))
	for i, s := range sales {
		ids[i], units[i] = s.ProductID, int32(s.Units) //nolint:gosec // an order's units are bounded
	}
	d := pgtype.Date{Time: day.UTC().Truncate(24 * time.Hour), Valid: true}
	if add {
		if err = q.AddProductSales(ctx, ordersdb.AddProductSalesParams{StoreID: store, ProductIds: ids, Day: d, Units: units}); err != nil {
			return fmt.Errorf("orders store: add sales: %w", err)
		}
		if err = q.MarkSaleCounted(ctx, ordersdb.MarkSaleCountedParams{StoreID: store, OrderID: orderID}); err != nil {
			return fmt.Errorf("orders store: mark counted: %w", err)
		}
		return nil
	}
	if err = q.SubtractProductSales(ctx, ordersdb.SubtractProductSalesParams{StoreID: store, ProductIds: ids, Day: d, Units: units}); err != nil {
		return fmt.Errorf("orders store: take back sales: %w", err)
	}
	if err = q.UnmarkSaleCounted(ctx, ordersdb.UnmarkSaleCountedParams{StoreID: store, OrderID: orderID}); err != nil {
		return fmt.Errorf("orders store: unmark counted: %w", err)
	}
	return nil
}

// TopSelling returns the store's products by units sold since a day,
// most first, at most limit.
func TopSelling(ctx context.Context, tx Tx, since time.Time, limit int) ([]ulid.ID, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := ordersdb.New(tx).TopSellingProducts(ctx, ordersdb.TopSellingProductsParams{StoreID: store,
		Since: pgtype.Date{Time: since.UTC().Truncate(24 * time.Hour), Valid: true}, Lim: int32(limit)}) //nolint:gosec // bounded by the caller
	if err != nil {
		return nil, fmt.Errorf("orders store: top selling: %w", err)
	}
	out := make([]ulid.ID, len(rows))
	for i, r := range rows {
		out[i] = r.ProductID
	}
	return out, nil
}

// PruneSales drops days before a day: no read reaches them.
func PruneSales(ctx context.Context, tx Tx, before time.Time) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = ordersdb.New(tx).PruneProductSales(ctx, ordersdb.PruneProductSalesParams{StoreID: store,
		Before: pgtype.Date{Time: before.UTC().Truncate(24 * time.Hour), Valid: true}}); err != nil {
		return fmt.Errorf("orders store: prune sales: %w", err)
	}
	return nil
}
