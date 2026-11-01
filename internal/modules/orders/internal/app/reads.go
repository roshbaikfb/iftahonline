package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/text"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// MaxBulk bounds one bulk transition.
const MaxBulk = 100

// Get returns an order with its lines.
func (s *Service) Get(ctx context.Context, id ulid.ID) (domain.Order, error) {
	d, err := s.detail(ctx, id, false)
	return d.Order, err
}

// Detail is an order with its lines and timeline.
type Detail struct {
	Order  domain.Order
	Events []domain.Event
}

// GetDetail returns an order with its lines and timeline, in a constant
// number of queries, each on the order's partition.
func (s *Service) GetDetail(ctx context.Context, id ulid.ID) (Detail, error) {
	return s.detail(ctx, id, true)
}

func (s *Service) detail(ctx context.Context, id ulid.ID, events bool) (Detail, error) {
	var d Detail
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		o, err := store.Get(ctx, tx, id, false)
		if err != nil {
			return err
		}
		if o.Lines, err = store.Lines(ctx, tx, id, o.Total.Currency()); err != nil {
			return err
		}
		d.Order = o
		if events {
			d.Events, err = store.Events(ctx, tx, id)
		}
		return err
	})
	return d, notFound(err)
}

// GetTx returns an order with its lines in the caller's transaction,
// locked until it ends: for integrations that read an order and then
// move it, or record its payment, in one transaction (no second
// connection, and no change in between).
func (s *Service) GetTx(ctx context.Context, tx pgx.Tx, id ulid.ID) (domain.Order, error) {
	o, err := store.Get(ctx, tx, id, true)
	if err != nil {
		return domain.Order{}, notFound(err)
	}
	if o.Lines, err = store.Lines(ctx, tx, id, o.Total.Currency()); err != nil {
		return domain.Order{}, err
	}
	return o, nil
}

// Page is one page of orders, newest first.
type Page struct {
	Items []domain.Order // without lines
	Next  *ulid.ID       // nil on the last page
}

// List returns orders newest first, filtered by q, in one query.
func (s *Service) List(ctx context.Context, q domain.Query) (Page, error) {
	info, err := s.stores(ctx)
	if err != nil {
		return Page{}, err
	}
	f, err := domain.ResolveQuery(q, info.Country)
	if err != nil {
		return Page{}, err
	}
	want := f.Limit
	f.Limit++ // one extra row tells whether there is a next page
	var items []domain.Order
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		items, txErr = store.List(ctx, tx, f)
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

// Before returns up to limit (at most domain.MaxListLimit) of the
// store's orders with IDs below before, newest first and without lines,
// in one query: the risk module reads a store's past through it
// (ADR-0065). A zero before starts from the newest order.
func (s *Service) Before(ctx context.Context, before ulid.ID, limit int) ([]domain.Order, error) {
	if limit < 1 || limit > domain.MaxListLimit {
		return nil, fmt.Errorf("orders: limit %d out of range", limit)
	}
	f := domain.Filter{From: time.Unix(0, 0).UTC(), To: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), Before: before, Limit: limit}
	if before.IsZero() {
		f.Before = domain.MaxID
	} else {
		f.To = domain.CreatedAtOf(before).Add(time.Millisecond)
	}
	var out []domain.Order
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		out, txErr = store.List(ctx, tx, f)
		return txErr
	})
	return out, err
}

// Counts returns every status with its number of orders (0 when none),
// for the dashboard's tabs and badge, in one query.
func (s *Service) Counts(ctx context.Context) (map[domain.Status]int, error) {
	var got map[domain.Status]int
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		got, txErr = store.CountByStatus(ctx, tx)
		return txErr
	})
	if err != nil {
		return nil, err
	}
	out := make(map[domain.Status]int, len(domain.Statuses()))
	for _, st := range domain.Statuses() {
		out[st] = got[st]
	}
	return out, nil
}

// Stats sums the store's orders for the dashboard home over r, by the
// store's time zone (the owner, 2026-10-27).
func (s *Service) Stats(ctx context.Context, r domain.StatsRange) (domain.Stats, error) {
	info, err := s.stores(ctx)
	if err != nil {
		return domain.Stats{}, err
	}
	loc := info.Location
	if loc == nil {
		loc = time.UTC
	}
	w := r.Windows(s.now(), loc)
	var st domain.Stats
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		st, txErr = store.Stats(ctx, tx, w, loc.String())
		return txErr
	})
	st.Range, st.Currency = r, string(info.Currency)
	return st, err
}

// maxTrackedOrders is how far back one phone's orders are searched for a
// code: a shopper tracks a recent order.
const maxTrackedOrders = domain.MaxListLimit

// Track finds the order a shopper names by its code and the phone it was
// placed with, in any usual format (the storefront's order tracking).
// It reads that phone's recent orders through their index and matches
// the code. Anything else (a bad code or phone, no such order) is
// ErrOrderNotFound, so a caller cannot tell which part was wrong. The
// order comes without its lines.
func (s *Service) Track(ctx context.Context, code, rawPhone string) (domain.Order, error) {
	want, ok := domain.NormalizeCode(code)
	if !ok || strings.TrimSpace(rawPhone) == "" {
		return domain.Order{}, ErrOrderNotFound
	}
	page, err := s.List(ctx, domain.Query{Phone: rawPhone, Limit: maxTrackedOrders})
	if errs.KindOf(err) == errs.KindValidation {
		return domain.Order{}, ErrOrderNotFound
	}
	if err != nil {
		return domain.Order{}, err
	}
	for _, o := range page.Items {
		if o.Code() == want {
			return o, nil
		}
	}
	return domain.Order{}, ErrOrderNotFound
}

// BulkResult is one order's outcome in a bulk transition.
type BulkResult struct {
	ID    ulid.ID
	Order domain.Order // set on success
	Err   error
}

// BulkTransition moves each order to status to, each in its own
// transaction: one order that cannot move does not hold back the others
// (bulk confirm). Results come in the order of ids.
func (s *Service) BulkTransition(ctx context.Context, ids []ulid.ID, to domain.Status, note, reason string) ([]BulkResult, error) {
	if len(ids) == 0 || len(ids) > MaxBulk {
		return nil, errs.Validation(errs.FieldError{Field: "order_ids", Code: "count", Message: "send 1 to 100 orders"})
	}
	seen := make(map[ulid.ID]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			return nil, errs.Validation(errs.FieldError{Field: "order_ids", Code: "duplicate", Message: "each order may appear once"})
		}
		seen[id] = true
	}
	if _, err := cleanNote(note); err != nil {
		return nil, err
	}
	if _, err := domain.ParseReason(reason, to); err != nil {
		return nil, err
	}
	out := make([]BulkResult, len(ids))
	for i, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		o, err := s.TransitionWithReason(ctx, id, to, note, reason, 0)
		out[i] = BulkResult{ID: id, Order: o, Err: err}
	}
	return out, nil
}

func cleanNote(note string) (string, error) {
	return text.Clean(note, "note", 0, domain.MaxNotesLen)
}

// Conversion is an order as its server-side conversion needs it
// (ADR-0043): with its lines, and its signals; ok is false when the
// order has no signals (none were allowed, or their month passed).
func (s *Service) Conversion(ctx context.Context, id ulid.ID) (o domain.Order, sig domain.Signals, ok bool, err error) {
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if o, err = store.Get(ctx, tx, id, false); err != nil {
			return err
		}
		if o.Lines, err = store.Lines(ctx, tx, id, o.Total.Currency()); err != nil {
			return err
		}
		sig, ok, err = store.Signals(ctx, tx, o)
		return err
	})
	return o, sig, ok, notFound(err)
}
