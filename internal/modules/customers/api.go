// Package customers owns a store's customers: one per phone number,
// their order count and the store's block list (ADR-0020). This file is
// the module's only exported surface (ADR-0001); see
// docs/modules/customers.md.
package customers

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/customers/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/customers/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
)

// Customer is one phone number's history with one store.
type Customer = domain.Customer

// Errors. Match with errors.Is.
var (
	// ErrCustomerBlocked refuses an order. Shoppers must only ever see a
	// generic refusal, never that they are blocked.
	ErrCustomerBlocked  = app.ErrCustomerBlocked
	ErrCustomerNotFound = app.ErrCustomerNotFound
)

// Customers is what the orders module uses. Every method acts on the
// store in ctx (tenant.FromContext).
type Customers interface {
	// Blocked reports whether p may not order. It lets an order be
	// refused before any work is done; RecordOrderTx checks again.
	Blocked(ctx context.Context, p phone.E164) (bool, error)
	// RecordOrderTx records an order by p, named name, in the order's
	// tenant transaction (ADR-0007): it creates the customer or updates
	// their latest name and order count. A blocked customer is
	// ErrCustomerBlocked and the caller rolls back.
	RecordOrderTx(ctx context.Context, tx pgx.Tx, p phone.E164, name string) (Customer, error)
}

// RegionSource returns the phone region of the store in ctx; numbers
// typed without a country code are read in it.
type RegionSource = app.RegionSource

var _ Customers = (*app.Service)(nil)
