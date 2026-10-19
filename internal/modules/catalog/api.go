// Package catalog owns products, variants, stock and categories
// (ADR-0011). This file is the module's only exported surface (ADR-0001);
// see docs/modules/catalog.md.
package catalog

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// DTOs. They alias the domain types so callers get the same rules
// without a second, drifting copy.
type (
	// SellableVariant is a variant of an active product with its price
	// and stock: what checkout needs to sell it.
	SellableVariant = domain.SellableVariant
	// Stock is a variant's inventory level.
	Stock = domain.Stock
	// ReserveRequest holds stock for one ref (a checkout or order ID).
	ReserveRequest = domain.ReserveRequest
	// ReserveLine is one variant and quantity in a ReserveRequest.
	ReserveLine = domain.ReserveLine
	// Reservation is the stock held for one ref.
	Reservation = domain.Reservation
	// ReservationStatus is a reservation's state.
	ReservationStatus = domain.ReservationStatus
	// StockError names the variant a reservation failed on; it matches
	// ErrInsufficientStock or ErrVariantNotSellable with errors.Is.
	StockError = app.StockError
)

// Reservation statuses.
const (
	ReservationActive   = domain.ReservationActive
	ReservationReleased = domain.ReservationReleased
	ReservationExpired  = domain.ReservationExpired
)

// DefaultReservationTTL is how long stock is held when a request does not
// say.
const DefaultReservationTTL = domain.DefaultReservationTTL

// Errors. Match with errors.Is.
var (
	ErrVariantNotFound     = app.ErrVariantNotFound
	ErrVariantNotSellable  = app.ErrVariantNotSellable
	ErrInsufficientStock   = app.ErrInsufficientStock
	ErrReservationRefUsed  = app.ErrReservationRefUsed
	ErrReservationNotFound = app.ErrReservationNotFound
)

// Catalog is what other modules (checkout, orders) use. Every method acts
// on the store in ctx (tenant.FromContext).
type Catalog interface {
	// GetSellableVariant returns a variant of an active, live product, or
	// ErrVariantNotFound.
	GetSellableVariant(ctx context.Context, variantID ulid.ID) (SellableVariant, error)
	// ReserveStock holds stock for every line, all or nothing, until
	// ReleaseStock or the TTL (default 15 minutes). It never oversells,
	// whatever the concurrency. Repeating a ref with the same lines
	// returns the existing reservation.
	ReserveStock(ctx context.Context, req ReserveRequest) (Reservation, error)
	// ReleaseStock gives back the stock held for ref. It is idempotent;
	// an unknown ref is ErrReservationNotFound.
	ReleaseStock(ctx context.Context, ref string) error
}

// CurrencySource returns the currency of the store in ctx. cmd/platform
// adapts the tenancy module to it.
type CurrencySource = app.CurrencySource

// CurrencyFunc adapts a function to CurrencySource.
type CurrencyFunc func(ctx context.Context) (money.Currency, error)

// StoreCurrency implements CurrencySource.
func (f CurrencyFunc) StoreCurrency(ctx context.Context) (money.Currency, error) { return f(ctx) }

var _ Catalog = (*app.Service)(nil)
