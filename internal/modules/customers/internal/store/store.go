// Package store adapts the sqlc queries in customersdb to domain types.
// Every function runs on a transaction opened by postgres.InTenantTx and
// takes the store ID from tenant.FromContext, never from its arguments
// (CLAUDE.md rule 5); RLS checks it a second time.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/customers/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/customers/internal/store/customersdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Tx is the tenant transaction the functions run on.
type Tx = customersdb.DBTX

func current(ctx context.Context) (ulid.ID, error) {
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return ulid.ID{}, fmt.Errorf("customers store: %w", err)
	}
	return id.ID, nil
}

// RecordOrder creates or updates the customer of phone for a new order.
func RecordOrder(ctx context.Context, tx Tx, id ulid.ID, p phone.E164, name string) (domain.Customer, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Customer{}, err
	}
	row, err := customersdb.New(tx).RecordOrder(ctx, customersdb.RecordOrderParams{
		ID: id, StoreID: store, Phone: p, Name: name, NameKey: domain.NameKey(name),
	})
	if err != nil {
		return domain.Customer{}, fmt.Errorf("customers store: record order: %w", err)
	}
	return toCustomer(row), nil
}

// Get returns a customer, or domain.ErrNotFound.
func Get(ctx context.Context, tx Tx, id ulid.ID) (domain.Customer, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Customer{}, err
	}
	row, err := customersdb.New(tx).GetCustomer(ctx, customersdb.GetCustomerParams{StoreID: store, ID: id})
	return one(row, err, "get")
}

// GetByPhone returns the customer of a phone, or domain.ErrNotFound.
func GetByPhone(ctx context.Context, tx Tx, p phone.E164) (domain.Customer, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Customer{}, err
	}
	row, err := customersdb.New(tx).GetCustomerByPhone(ctx, customersdb.GetCustomerByPhoneParams{StoreID: store, Phone: p})
	return one(row, err, "get by phone")
}

// List returns one page of customers, newest first.
func List(ctx context.Context, tx Tx, l domain.Lookup) ([]domain.Customer, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	before := ulid.ID{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	if l.After != nil {
		before = *l.After
	}
	p := customersdb.ListCustomersParams{StoreID: store, BeforeID: before, OnlyBlocked: l.Blocked, Lim: int32(l.Limit)} //nolint:gosec // bounded by domain.MaxListLimit
	if l.Phone != nil {
		s := l.Phone.Unmasked()
		p.Phone = &s
	}
	if l.NamePrefix != "" {
		p.NamePrefix = &l.NamePrefix
	}
	rows, err := customersdb.New(tx).ListCustomers(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("customers store: list: %w", err)
	}
	out := make([]domain.Customer, len(rows))
	for i, r := range rows {
		out[i] = toCustomer(r)
	}
	return out, nil
}

// BlockPhone blocks a phone, creating its customer if needed.
func BlockPhone(ctx context.Context, tx Tx, id ulid.ID, p phone.E164, reason string) (domain.Customer, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Customer{}, err
	}
	row, err := customersdb.New(tx).BlockPhone(ctx, customersdb.BlockPhoneParams{ID: id, StoreID: store, Phone: p, Reason: reason})
	if err != nil {
		return domain.Customer{}, fmt.Errorf("customers store: block phone: %w", err)
	}
	return toCustomer(row), nil
}

// Block blocks a customer, or returns domain.ErrNotFound.
func Block(ctx context.Context, tx Tx, id ulid.ID, reason string) (domain.Customer, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Customer{}, err
	}
	row, err := customersdb.New(tx).BlockCustomer(ctx, customersdb.BlockCustomerParams{StoreID: store, ID: id, Reason: reason})
	return one(row, err, "block")
}

// Unblock lifts a block, or returns domain.ErrNotFound.
func Unblock(ctx context.Context, tx Tx, id ulid.ID) (domain.Customer, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Customer{}, err
	}
	row, err := customersdb.New(tx).UnblockCustomer(ctx, customersdb.UnblockCustomerParams{StoreID: store, ID: id})
	return one(row, err, "unblock")
}

func one(row customersdb.Customer, err error, op string) (domain.Customer, error) {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.Customer{}, domain.ErrNotFound
	case err != nil:
		return domain.Customer{}, fmt.Errorf("customers store: %s: %w", op, err)
	}
	return toCustomer(row), nil
}

func toCustomer(r customersdb.Customer) domain.Customer {
	return domain.Customer{
		ID: r.ID, Phone: r.Phone, Name: r.Name, OrdersCount: int(r.OrdersCount), LastOrderAt: r.LastOrderAt,
		BlockedAt: r.BlockedAt, BlockReason: r.BlockReason, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}
