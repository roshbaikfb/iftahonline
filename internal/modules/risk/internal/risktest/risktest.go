// Package risktest is the risk module's test fixture: orders in memory
// per store (orders.Outcomes), stores in PostgreSQL, and phone numbers
// no other test uses. Production code must not import it.
package risktest

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Orders is orders.Outcomes in memory, per store.
type Orders struct {
	IDs    *ulid.Generator
	Now    time.Time
	mu     sync.Mutex
	orders map[tenant.StoreID][]orders.Order // oldest first
}

// NewOrders returns an empty store of orders dated from now.
func NewOrders(ids *ulid.Generator, now time.Time) *Orders {
	return &Orders{IDs: ids, Now: now, orders: map[tenant.StoreID][]orders.Order{}}
}

// Put adds or replaces an order of the store in ctx. Without a store,
// the order belongs to none and no store finds it.
func (f *Orders) Put(ctx context.Context, o orders.Order) {
	s, err := tenant.FromContext(ctx)
	if err != nil {
		s = tenant.StoreID{}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, have := range f.orders[s] {
		if have.ID == o.ID {
			f.orders[s][i] = o
			return
		}
	}
	f.orders[s] = append(f.orders[s], o)
}

// Order puts an order of p in the store in ctx, placed daysAgo days ago
// and now in status st with reason.
func (f *Orders) Order(ctx context.Context, p phone.E164, daysAgo int, st orders.Status, reason orders.OutcomeReason) orders.Order {
	at := f.Now.Add(-time.Duration(daysAgo) * 24 * time.Hour)
	o := orders.Order{ID: f.IDs.New(), Status: st, OutcomeReason: reason, CreatedAt: at, StatusChangedAt: at.Add(time.Hour)}
	o.Contact.Phone = p
	f.Put(ctx, o)
	return o
}

// Get implements orders.Outcomes.
func (f *Orders) Get(ctx context.Context, id ulid.ID) (orders.Order, error) {
	s, err := tenant.FromContext(ctx)
	if err != nil {
		return orders.Order{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, o := range f.orders[s] {
		if o.ID == id {
			return o, nil
		}
	}
	return orders.Order{}, orders.ErrOrderNotFound
}

// Before implements orders.Outcomes.
func (f *Orders) Before(ctx context.Context, before ulid.ID, limit int) ([]orders.Order, error) {
	s, err := tenant.FromContext(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []orders.Order
	for _, o := range slices.Backward(f.orders[s]) {
		if (before.IsZero() || bytes.Compare(o.ID[:], before[:]) < 0) && len(out) < limit {
			out = append(out, o)
		}
	}
	return out, nil
}

// BeforeWithLines implements orders.Outcomes: its orders are whole.
func (f *Orders) BeforeWithLines(ctx context.Context, before ulid.ID, limit int) ([]orders.Order, error) {
	return f.Before(ctx, before, limit)
}

// Store creates a store and returns a context in it.
func Store(t *testing.T, db postgres.Beginner, ids *ulid.Generator) (context.Context, tenant.StoreID) {
	t.Helper()
	id := tenant.StoreID{ID: ids.New()}
	ctx := tenant.WithStore(t.Context(), id)
	if err := postgres.InTenantTx(ctx, db, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
			id.ID, "rsk-"+strings.ToLower(id.String()[14:]))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return ctx, id
}

// Phone returns an Egyptian mobile number no other test uses.
func Phone(t *testing.T, ids *ulid.Generator) phone.E164 {
	t.Helper()
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, ids.New().String())
	for len(digits) < 8 {
		digits += "7"
	}
	p, err := phone.Parse("010"+digits[len(digits)-8:], phone.Region("EG"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}
