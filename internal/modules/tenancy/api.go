// Package tenancy owns stores and their domains, and resolves a request's
// Host to its store (ADR-0004). This file is the module's only exported
// surface (ADR-0001); see docs/modules/tenancy.md.
package tenancy

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// DTOs. They alias the domain types so callers get the same validation
// rules without a second, drifting copy.
type (
	// Store is a merchant's shop: the tenant.
	Store = domain.Store
	// NewStore is the input for CreateStoreTx.
	NewStore = domain.NewStore
	// StoreSettings is what the merchant can change.
	StoreSettings = domain.StoreSettings
	// StoreStatus is a store's lifecycle state.
	StoreStatus = domain.StoreStatus
	// Domain is a host that routes to a store.
	Domain = domain.Domain
	// Resolution is the store and status a host resolves to.
	Resolution = domain.Resolution
)

// Store statuses.
const (
	StatusActive    = domain.StatusActive
	StatusSuspended = domain.StatusSuspended
	StatusClosed    = domain.StatusClosed
)

// ErrUnknownHost means no store is reachable at a host.
var ErrUnknownHost = app.ErrUnknownHost

// Service manages the store in ctx (tenant.FromContext).
type Service interface {
	// CreateStoreTx creates the store whose ID is the tenant in ctx, with
	// its verified primary subdomain, in the caller's tenant transaction.
	CreateStoreTx(ctx context.Context, tx pgx.Tx, in NewStore) (Store, error)
	GetStore(ctx context.Context) (Store, error)
	UpdateSettings(ctx context.Context, in StoreSettings) (Store, error)
	// SetStatus is for the platform (suspension) and the owner (closing).
	SetStatus(ctx context.Context, status StoreStatus) (Store, error)
	ListDomains(ctx context.Context) ([]Domain, error)
	AddCustomDomain(ctx context.Context, host string) (Domain, error)
	VerifyDomain(ctx context.Context, id ulid.ID) (Domain, error)
	RemoveDomain(ctx context.Context, id ulid.ID) error
	SetPrimaryDomain(ctx context.Context, id ulid.ID) (Domain, error)
}

// HostResolver maps a raw Host header to its store. A cache hit does no
// I/O.
type HostResolver interface {
	Resolve(ctx context.Context, host string) (Resolution, error)
}

var (
	_ Service      = (*app.Service)(nil)
	_ HostResolver = (*app.Resolver)(nil)
)
