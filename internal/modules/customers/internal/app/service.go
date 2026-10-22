// Package app holds the customers use cases: the record orders keep of
// who ordered (RecordOrderTx, in the order's transaction), and the
// merchant's list, lookup and block list. Merchant writes are audited in
// the same transaction, without the phone number or the reason (the
// audit log is kept for years and shown to staff).
package app

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/customers/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/customers/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Errors. Match with errors.Is.
var (
	ErrCustomerNotFound = errs.NotFound("customer_not_found", "customer not found")
	// ErrCustomerBlocked refuses an order. Callers facing shoppers must
	// not reveal it (ADR-0020): they show a generic refusal.
	ErrCustomerBlocked = errs.Conflict("customer_blocked", "the store does not accept orders from this customer")
	errBadPhone        = errs.Validation(errs.FieldError{Field: "phone", Code: "invalid", Message: "not a supported mobile number"})
)

// RegionSource returns the phone region of the store in ctx: numbers
// typed without a country code are read in it. cmd/platform derives it
// from the store's currency (platform/geo).
type RegionSource func(ctx context.Context) (phone.Region, error)

// Service implements the use cases.
type Service struct {
	db     postgres.Beginner
	ids    *ulid.Generator
	audit  audit.Recorder
	region RegionSource
}

// Deps are the service's dependencies.
type Deps struct {
	DB     postgres.Beginner
	IDs    *ulid.Generator
	Audit  audit.Recorder
	Region RegionSource
}

// NewService builds the service. A nil Audit discards.
func NewService(d Deps) *Service {
	if d.Audit == nil {
		d.Audit = audit.Nop{}
	}
	return &Service{db: d.DB, ids: d.IDs, audit: d.Audit, region: d.Region}
}

// RecordOrderTx records an order by p in the caller's tenant
// transaction: it creates the customer or updates their latest name and
// order count. A blocked customer is ErrCustomerBlocked, and the caller
// rolls back (nothing is recorded).
func (s *Service) RecordOrderTx(ctx context.Context, tx pgx.Tx, p phone.E164, name string) (domain.Customer, error) {
	if p.IsZero() {
		return domain.Customer{}, errBadPhone
	}
	name, err := domain.CleanName(name)
	if err != nil {
		return domain.Customer{}, err
	}
	c, err := store.RecordOrder(ctx, tx, s.ids.New(), p, name)
	if err != nil {
		return domain.Customer{}, err
	}
	if c.Blocked() {
		return domain.Customer{}, ErrCustomerBlocked
	}
	return c, nil
}

// Blocked reports whether p is blocked in the store in ctx. It lets an
// order be refused before any work; RecordOrderTx checks again.
func (s *Service) Blocked(ctx context.Context, p phone.E164) (bool, error) {
	var blocked bool
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		c, err := store.GetByPhone(ctx, tx, p)
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		blocked = err == nil && c.Blocked()
		return err
	})
	return blocked, err
}

// Get returns a customer of the store in ctx.
func (s *Service) Get(ctx context.Context, id ulid.ID) (domain.Customer, error) {
	var c domain.Customer
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		c, txErr = store.Get(ctx, tx, id)
		return txErr
	})
	return c, notFound(err)
}

// Page is one page of customers.
type Page struct {
	Items []domain.Customer
	Next  *ulid.ID // nil on the last page
}

// List returns customers newest first, filtered by q.
func (s *Service) List(ctx context.Context, q domain.Query) (Page, error) {
	region, err := s.region(ctx)
	if err != nil {
		return Page{}, err
	}
	l, err := domain.ResolveQuery(q, region)
	if err != nil {
		return Page{}, err
	}
	want := l.Limit
	l.Limit++ // one extra row tells whether there is a next page
	var items []domain.Customer
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		items, txErr = store.List(ctx, tx, l)
		return txErr
	})
	if err != nil {
		return Page{}, err
	}
	p := Page{Items: items}
	if len(items) > want {
		p.Items = items[:want]
		next := p.Items[want-1].ID
		p.Next = &next
	}
	return p, nil
}

// BlockPhone blocks a phone number (typed in any accepted format),
// whether or not it ever ordered.
func (s *Service) BlockPhone(ctx context.Context, raw, reason string) (domain.Customer, error) {
	region, err := s.region(ctx)
	if err != nil {
		return domain.Customer{}, err
	}
	p, err := phone.Parse(raw, region)
	if err != nil {
		return domain.Customer{}, errBadPhone
	}
	if reason, err = domain.CleanReason(reason); err != nil {
		return domain.Customer{}, err
	}
	var c domain.Customer
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		if c, txErr = store.BlockPhone(ctx, tx, s.ids.New(), p, reason); txErr != nil {
			return txErr
		}
		return s.record(ctx, tx, "customer.blocked", c.ID)
	})
	return c, err
}

// Block blocks a customer.
func (s *Service) Block(ctx context.Context, id ulid.ID, reason string) (domain.Customer, error) {
	reason, err := domain.CleanReason(reason)
	if err != nil {
		return domain.Customer{}, err
	}
	return s.write(ctx, id, "customer.blocked", func(tx pgx.Tx) (domain.Customer, error) { return store.Block(ctx, tx, id, reason) })
}

// Unblock lifts a customer's block.
func (s *Service) Unblock(ctx context.Context, id ulid.ID) (domain.Customer, error) {
	return s.write(ctx, id, "customer.unblocked", func(tx pgx.Tx) (domain.Customer, error) { return store.Unblock(ctx, tx, id) })
}

func (s *Service) write(ctx context.Context, id ulid.ID, action string, fn func(pgx.Tx) (domain.Customer, error)) (domain.Customer, error) {
	var c domain.Customer
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		if c, txErr = fn(tx); txErr != nil {
			return txErr
		}
		return s.record(ctx, tx, action, id)
	})
	return c, notFound(err)
}

func (s *Service) record(ctx context.Context, tx pgx.Tx, action string, id ulid.ID) error {
	return s.audit.RecordTx(ctx, tx, audit.Entry{Action: action, TargetType: "customer", TargetID: id.String()})
}

func notFound(err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return ErrCustomerNotFound
	}
	return err
}
