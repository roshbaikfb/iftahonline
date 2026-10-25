// Package catalog owns products, variants, stock and categories
// (ADR-0011). This file is the module's only exported surface (ADR-0001);
// see docs/modules/catalog.md.
package catalog

import (
	"context"

	"github.com/jackc/pgx/v5"

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
	// StorefrontSearch is a shopper's search: text and a page.
	StorefrontSearch = app.StorefrontSearch
	// StorefrontSearchResult is a page of a shopper's search.
	StorefrontSearchResult = app.StorefrontSearchResult
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
	// CategoryInput creates a category.
	CategoryInput = domain.CategoryInput
	// ProductInput creates a product; VariantInput is one of its variants.
	ProductInput = domain.ProductInput
	VariantInput = domain.VariantInput
	// SampleProduct is a starter template's product and picture.
	SampleProduct = app.SampleProduct

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
	// MaxShownLeft is the most units left StorefrontVariant.Left reports.
	MaxShownLeft = domain.MaxShownLeft
	// ShopSearchPageSize and MaxShopSearchLen bound a shopper's search.
	ShopSearchPageSize = app.ShopSearchPageSize
	MaxShopSearchLen   = app.MaxShopSearchLen
	MaxShopSearchPage  = app.MaxShopSearchPage
)

// ErrSearchUnavailable means the storefront has no search configured.
var ErrSearchUnavailable = app.ErrSearchUnavailable

// Reservation statuses.
const (
	ReservationActive   = domain.ReservationActive
	ReservationReleased = domain.ReservationReleased
	ReservationExpired  = domain.ReservationExpired
	// ReservationCommitted: the units shipped (on_hand dropped).
	ReservationCommitted = domain.ReservationCommitted
	// ReservationReturned: committed units are back on hand.
	ReservationReturned = domain.ReservationReturned
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
	ErrReservationState    = app.ErrReservationState
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

	// The …Tx methods run in the caller's tenant transaction (ADR-0007),
	// so an order's state and its stock commit or roll back together
	// (ADR-0020). Each is idempotent; a ref in a state that does not
	// allow the step is ErrReservationState.

	// ReserveStockTx is ReserveStock in tx. With req.Hold the stock is
	// held without expiry until released or committed.
	ReserveStockTx(ctx context.Context, tx pgx.Tx, req ReserveRequest) (Reservation, error)
	// ReleaseStockTx is ReleaseStock in tx (an order cancelled before it
	// shipped).
	ReleaseStockTx(ctx context.Context, tx pgx.Tx, ref string) error
	// CommitStockTx ships the held units: on_hand and reserved both drop.
	CommitStockTx(ctx context.Context, tx pgx.Tx, ref string) error
	// RestockTx puts committed units back on hand (a returned order).
	RestockTx(ctx context.Context, tx pgx.Tx, ref string) error
}

// Storefront is the read side the storefront renders: active, live
// products only, with signed image URLs and no stock levels. Every method
// acts on the store in ctx and runs a constant number of queries.
// Starter is what the setup wizard's starter template uses (ADR-0034).
// Every method acts on the store in ctx and is idempotent by slug: the
// template may run twice, and a merchant's own category or product with
// the same slug is left as it is.
type Starter interface {
	EnsureCategory(ctx context.Context, in CategoryInput) (ulid.ID, error)
	// EnsureSampleProduct adds a product marked as a sample: shown,
	// marked, never sellable, deleted with the store's other samples.
	EnsureSampleProduct(ctx context.Context, in SampleProduct) (ulid.ID, error)
}

var _ Starter = (*app.Service)(nil)

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
	// SellableVariants is Catalog.SellableVariants, for the cart.
	SellableVariants(ctx context.Context, ids []ulid.ID) ([]SellableVariant, error)
	// Search finds active products for shoppers, a page at a time;
	// ErrSearchUnavailable when the storefront has no search configured.
	Search(ctx context.Context, q StorefrontSearch) (StorefrontSearchResult, error)
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
