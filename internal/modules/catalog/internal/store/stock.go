package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/store/catalogdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// ErrBelowReserved means on_hand would drop below the units already held
// by reservations.
var ErrBelowReserved = errs.Conflict("stock_below_reserved", "on_hand cannot be lower than the units currently reserved")

// SetStock sets a live variant's stock level and returns it with the
// variant's product.
func SetStock(ctx context.Context, tx Tx, variantID ulid.ID, in domain.StockInput) (domain.Stock, ulid.ID, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Stock{}, ulid.ID{}, err
	}
	row, err := catalogdb.New(tx).SetInventoryLevel(ctx, catalogdb.SetInventoryLevelParams{
		StoreID: store, VariantID: variantID, OnHand: int32(in.OnHand), Tracked: in.Tracked, //nolint:gosec // bounded by domain.MaxOnHand
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.Stock{}, ulid.ID{}, domain.ErrNotFound
	case postgres.IsCheckViolation(err, "inventory_levels_bounds"):
		return domain.Stock{}, ulid.ID{}, ErrBelowReserved
	case err != nil:
		return domain.Stock{}, ulid.ID{}, fmt.Errorf("catalog store: set stock: %w", err)
	}
	return domain.Stock{Tracked: row.Tracked, OnHand: int(row.OnHand), Reserved: int(row.Reserved)}, row.ProductID, nil
}

// Reserved is the outcome of Reserve.
type Reserved struct {
	OK        bool    // false: the variant is not sellable or has too little stock
	Counted   bool    // the units were counted against tracked stock
	SoldOut   bool    // this reservation took the last available unit
	ProductID ulid.ID // set when OK
}

// Reserve holds qty units of a sellable variant.
func Reserve(ctx context.Context, tx Tx, variantID ulid.ID, qty int) (Reserved, error) {
	store, err := current(ctx)
	if err != nil {
		return Reserved{}, err
	}
	row, err := catalogdb.New(tx).ReserveInventory(ctx, catalogdb.ReserveInventoryParams{
		StoreID: store, VariantID: variantID, Qty: int32(qty), //nolint:gosec // bounded by domain.MaxReserveQty
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Reserved{}, nil
	}
	if err != nil {
		return Reserved{}, fmt.Errorf("catalog store: reserve: %w", err)
	}
	return Reserved{OK: true, Counted: row.Tracked, SoldOut: row.Tracked && row.Available == 0, ProductID: row.ProductID}, nil
}

// Unreserve gives back qty counted units of a variant. restocked reports
// that the variant had none available before.
func Unreserve(ctx context.Context, tx Tx, variantID ulid.ID, qty int) (productID ulid.ID, restocked bool, err error) {
	store, err := current(ctx)
	if err != nil {
		return ulid.ID{}, false, err
	}
	row, err := catalogdb.New(tx).UnreserveInventory(ctx, catalogdb.UnreserveInventoryParams{
		StoreID: store, VariantID: variantID, Qty: int32(qty), //nolint:gosec // bounded by domain.MaxReserveQty
	})
	if err != nil {
		return ulid.ID{}, false, fmt.Errorf("catalog store: unreserve: %w", err)
	}
	return row.ProductID, int(row.Available) == qty, nil
}

// ReservedLine is a reservation line as stored.
type ReservedLine struct {
	ID      ulid.ID
	Line    domain.ReserveLine
	Counted bool
}

// InsertReservation records the lines of ref.
func InsertReservation(ctx context.Context, tx Tx, ref string, expiresAt time.Time, lines []ReservedLine) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	p := catalogdb.InsertReservationsParams{
		StoreID: store, Ref: ref, ExpiresAt: expiresAt,
		Ids: make([]ulid.ID, len(lines)), VariantIds: make([]ulid.ID, len(lines)),
		Qtys: make([]int32, len(lines)), Counted: make([]bool, len(lines)),
	}
	for i, l := range lines {
		p.Ids[i], p.VariantIds[i], p.Counted[i] = l.ID, l.Line.VariantID, l.Counted
		p.Qtys[i] = int32(l.Line.Qty) //nolint:gosec // bounded by domain.MaxReserveQty
	}
	if err = catalogdb.New(tx).InsertReservations(ctx, p); err != nil {
		return fmt.Errorf("catalog store: insert reservation: %w", err)
	}
	return nil
}

// GetReservation returns the lines of ref, or ErrNotFound. Status and
// expiry are those of the lines (a ref's lines share them).
func GetReservation(ctx context.Context, tx Tx, ref string) (domain.Reservation, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Reservation{}, err
	}
	rows, err := catalogdb.New(tx).ListReservations(ctx, catalogdb.ListReservationsParams{StoreID: store, Ref: ref})
	if err != nil {
		return domain.Reservation{}, fmt.Errorf("catalog store: get reservation: %w", err)
	}
	if len(rows) == 0 {
		return domain.Reservation{}, domain.ErrNotFound
	}
	r := domain.Reservation{Ref: ref, Status: domain.ReservationStatus(rows[0].Status), ExpiresAt: rows[0].ExpiresAt,
		Lines: make([]domain.ReserveLine, len(rows))}
	for i, row := range rows {
		r.Lines[i] = domain.ReserveLine{VariantID: row.VariantID, Qty: int(row.Qty)}
	}
	return r, nil
}

// EndReservation moves ref's active lines to status (released or
// expired; onlyExpired keeps lines that have not expired yet) and returns
// the lines it ended, sorted by variant.
func EndReservation(ctx context.Context, tx Tx, ref string, status domain.ReservationStatus, onlyExpired bool) ([]ReservedLine, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := catalogdb.New(tx).EndReservations(ctx, catalogdb.EndReservationsParams{
		StoreID: store, Ref: ref, Status: string(status), OnlyExpired: onlyExpired,
	})
	if err != nil {
		return nil, fmt.Errorf("catalog store: end reservation: %w", err)
	}
	out := make([]ReservedLine, len(rows))
	for i, r := range rows {
		out[i] = ReservedLine{Line: domain.ReserveLine{VariantID: r.VariantID, Qty: int(r.Qty)}, Counted: r.Counted}
	}
	return out, nil
}

// GetSellableVariant returns a variant of an active, live product.
func GetSellableVariant(ctx context.Context, tx Tx, variantID ulid.ID) (domain.SellableVariant, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.SellableVariant{}, err
	}
	r, err := catalogdb.New(tx).GetSellableVariant(ctx, catalogdb.GetSellableVariantParams{StoreID: store, ID: variantID})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SellableVariant{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.SellableVariant{}, fmt.Errorf("catalog store: get sellable variant: %w", err)
	}
	return toSellable(catalogdb.ListSellableVariantsRow(r))
}

// ListSellableVariants returns the sellable variants among ids, in the
// order given, in one query.
func ListSellableVariants(ctx context.Context, tx Tx, ids []ulid.ID) ([]domain.SellableVariant, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := catalogdb.New(tx).ListSellableVariants(ctx, catalogdb.ListSellableVariantsParams{StoreID: store, Ids: ids})
	if err != nil {
		return nil, fmt.Errorf("catalog store: list sellable variants: %w", err)
	}
	out := make([]domain.SellableVariant, 0, len(rows))
	for _, r := range rows {
		v, convErr := toSellable(r)
		if convErr != nil {
			return nil, convErr
		}
		out = append(out, v)
	}
	return out, nil
}

func toSellable(r catalogdb.ListSellableVariantsRow) (domain.SellableVariant, error) {
	price, compareAt, err := prices(r.Currency, r.PriceMinor, r.CompareAtMinor)
	if err != nil {
		return domain.SellableVariant{}, err
	}
	return domain.SellableVariant{
		VariantID: r.ID, ProductID: r.ProductID, ProductTitle: r.ProductTitle, ProductSlug: r.ProductSlug,
		SKU: deref(r.Sku), OptionValues: r.OptionValues, Price: price, CompareAt: compareAt,
		Stock: domain.Stock{Tracked: r.Tracked, OnHand: int(r.OnHand), Reserved: int(r.Reserved)},
	}, nil
}
