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

	// StorefrontProduct is an active product as a shopper sees it.
	StorefrontProduct = app.StorefrontProduct
	// StorefrontVariant is a variant with its price and availability.
	StorefrontVariant = app.StorefrontVariant
	// StorefrontImage is a ready image with its delivery renditions.
	StorefrontImage = app.StorefrontImage
	// Rendition is one signed image URL with its served size.
	Rendition = app.Rendition
	// StorefrontQuery selects a page of products.
	StorefrontQuery = app.StorefrontQuery
	// StorefrontPage is one page of products, newest first.
	StorefrontPage = app.StorefrontPage
	// Option is a product dimension such as size, with its values.
	Option = domain.Option
	// Category is a node of the store's category tree.
	Category = domain.Category

	// ProductChanged is the catalog.product.changed event.
	ProductChanged = domain.ProductChanged
	// ProductDeleted is the catalog.product.deleted event.
	ProductDeleted = domain.ProductDeleted
	// CategoryChanged is the catalog.category.changed event.
	CategoryChanged = domain.CategoryChanged
)

// Event types the catalog publishes (ADR-0015).
const (
	EventProductChanged  = domain.EventProductChanged
	EventProductDeleted  = domain.EventProductDeleted
	EventCategoryChanged = domain.EventCategoryChanged
)

// Storefront limits.
const (
	MaxSellableIDs     = app.MaxSellableIDs
	MaxStorefrontLimit = app.MaxStorefrontLimit
	MaxStorefrontIDs   = app.MaxStorefrontIDs
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
	ErrProductNotFound     = app.ErrProductNotFound
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
	// SellableVariants is GetSellableVariant for many IDs (at most
	// MaxSellableIDs), in the order given, skipping the unsellable, in a
	// constant number of queries.
	SellableVariants(ctx context.Context, ids []ulid.ID) ([]SellableVariant, error)
	// ReserveStock holds stock for every line, all or nothing, until
	// ReleaseStock or the TTL (default 15 minutes). It never oversells,
	// whatever the concurrency. Repeating a ref with the same lines
	// returns the existing reservation.
	ReserveStock(ctx context.Context, req ReserveRequest) (Reservation, error)
	// ReleaseStock gives back the stock held for ref. It is idempotent;
	// an unknown ref is ErrReservationNotFound.
	ReleaseStock(ctx context.Context, ref string) error
}

// Storefront is the read side the storefront renders: active, live
// products only, with signed image URLs and no stock levels. Every method
// acts on the store in ctx and runs a constant number of queries.
type Storefront interface {
	// ProductBySlug returns the active product at slug, or
	// ErrProductNotFound.
	ProductBySlug(ctx context.Context, slug string) (StorefrontProduct, error)
	// ProductsByIDs returns the active products among ids in the order
	// given (at most MaxStorefrontIDs), skipping the others.
	ProductsByIDs(ctx context.Context, ids []ulid.ID) ([]StorefrontProduct, error)
	// ListProducts returns a page of active products, newest first.
	ListProducts(ctx context.Context, q StorefrontQuery) (StorefrontPage, error)
	// Categories returns the store's categories, by position.
	Categories(ctx context.Context) ([]Category, error)
}

// CurrencySource returns the currency of the store in ctx. cmd/platform
// adapts the tenancy module to it.
type CurrencySource = app.CurrencySource

// CurrencyFunc adapts a function to CurrencySource.
type CurrencyFunc func(ctx context.Context) (money.Currency, error)

// StoreCurrency implements CurrencySource.
func (f CurrencyFunc) StoreCurrency(ctx context.Context) (money.Currency, error) { return f(ctx) }

var (
	_ Catalog    = (*app.Service)(nil)
	_ Storefront = (*app.Storefront)(nil)
)
