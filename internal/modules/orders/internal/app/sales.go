package app

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Bestsellers' window, and how long counted days are kept (a few days
// beyond it, so a late cancellation still finds its day).
const (
	BestsellersWindow = 30 * 24 * time.Hour
	salesRetention    = 40 * 24 * time.Hour
	MaxBestsellers    = 48
)

// CountSales brings the bestsellers in line with an order's status
// (ADR-0033): a confirmed, ready, shipped or delivered order counts its
// units per product on its creation day; any other does not. It reads the
// order as it is now and a ledger of counted orders, under the order's
// row lock, so running twice, or for events seen out of order, changes
// nothing more.
func (s *Service) CountSales(ctx context.Context, orderID ulid.ID) error {
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		o, err := store.Get(ctx, tx, orderID, true)
		if err != nil {
			return err
		}
		counted, err := store.SaleCounted(ctx, tx, orderID)
		if err != nil {
			return err
		}
		want := domain.CountsAsSale(o.Status)
		if want == counted {
			return nil
		}
		if o.Lines, err = store.Lines(ctx, tx, orderID, o.Total.Currency()); err != nil {
			return err
		}
		units := map[ulid.ID]int{}
		var products []ulid.ID
		for _, l := range o.Lines {
			if _, ok := units[l.ProductID]; !ok {
				products = append(products, l.ProductID)
			}
			units[l.ProductID] += l.Qty
		}
		sales := make([]store.Sale, len(products))
		for i, p := range products {
			sales[i] = store.Sale{ProductID: p, Units: units[p]}
		}
		if err = store.CountSale(ctx, tx, orderID, o.CreatedAt, sales, want); err != nil {
			return err
		}
		return store.PruneSales(ctx, tx, s.now().Add(-salesRetention))
	})
	if notFound(err) == ErrOrderNotFound {
		return nil // gone (another store's event cannot reach here: RLS)
	}
	return err
}

// Bestsellers returns the store's products by units sold in confirmed
// orders over the last 30 days, most first, at most limit (≤ 48).
func (s *Service) Bestsellers(ctx context.Context, limit int) ([]ulid.ID, error) {
	limit = min(max(limit, 1), MaxBestsellers)
	var out []ulid.ID
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		out, txErr = store.TopSelling(ctx, tx, s.now().Add(-BestsellersWindow), limit)
		return txErr
	})
	return out, err
}
