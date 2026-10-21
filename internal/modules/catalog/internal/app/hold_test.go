package app_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// TestHeldReservationLifecycle walks an order's stock (ADR-0020): held
// without expiry in the order's transaction, committed when it ships,
// restocked when it comes back. Every step is idempotent, steps out of
// order are ErrReservationState, and a rolled-back transaction holds
// nothing.
func TestHeldReservationLifecycle(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p := mustCreate(f.ctxA, t, f, single("Held", 2, true))
	v := p.Variants[0]
	inTx := func(fn func(tx pgx.Tx) error) error { return postgres.InTenantTx(f.ctxA, f.pool, fn) }
	hold := domain.ReserveRequest{Ref: "order-held", Lines: []domain.ReserveLine{{VariantID: v.ID, Qty: 2}}, Hold: true}

	// The order failed after reserving: nothing stays held.
	rollback := errors.New("order insert failed")
	err := inTx(func(tx pgx.Tx) error {
		if _, err := f.svc.ReserveStockTx(f.ctxA, tx, hold); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	stock(t, f, v.ID, domain.Stock{Tracked: true, OnHand: 2, Reserved: 0})

	f.events.take()
	var r domain.Reservation
	if err = inTx(func(tx pgx.Tx) (txErr error) { r, txErr = f.svc.ReserveStockTx(f.ctxA, tx, hold); return txErr }); err != nil {
		t.Fatal(err)
	}
	if r.Status != domain.ReservationActive || !r.Held || !r.ExpiresAt.IsZero() {
		t.Fatalf("held reservation %+v", r)
	}
	stock(t, f, v.ID, domain.Stock{Tracked: true, OnHand: 2, Reserved: 2})
	if got := f.events.take(); !slices.Equal(got, []string{changed(p.ID, domain.ReasonStock)}) {
		t.Fatalf("sold out events = %v", got)
	}
	// The retry returns it; the same ref unheld is another reservation.
	if r, err = f.svc.ReserveStock(f.ctxA, hold); err != nil || !r.Held {
		t.Fatalf("retry: %+v %v", r, err)
	}
	unheld := hold
	unheld.Hold = false
	if _, err = f.svc.ReserveStock(f.ctxA, unheld); !errors.Is(err, app.ErrReservationRefUsed) {
		t.Fatalf("unheld under a held ref: %v", err)
	}
	// No expiry: no job is scheduled and the expiry worker leaves it.
	var scheduled int
	if err = inTx(func(tx pgx.Tx) error {
		return tx.QueryRow(f.ctxA, `SELECT count(*) FROM river_job WHERE kind = 'catalog.expire_reservation' AND args->>'ref' = $1`, hold.Ref).Scan(&scheduled)
	}); err != nil || scheduled != 0 {
		t.Fatalf("expiry jobs = %d (%v), want 0", scheduled, err)
	}
	expire := &app.ExpireWorker{DB: f.pool, Events: app.Events{Pub: f.events, IDs: f.ids}}
	if err = expire.Work(f.ctxA, &river.Job[app.ExpireReservationArgs]{Args: app.ExpireReservationArgs{
		TenantArgs: jobs.TenantArgs{StoreID: mustStore(f.ctxA, t)}, Ref: hold.Ref}}); err != nil {
		t.Fatal(err)
	}
	stock(t, f, v.ID, domain.Stock{Tracked: true, OnHand: 2, Reserved: 2})

	// Restock before commit is out of order.
	if err = inTx(func(tx pgx.Tx) error { return f.svc.RestockTx(f.ctxA, tx, hold.Ref) }); !errors.Is(err, app.ErrReservationState) {
		t.Fatalf("restock before commit: %v", err)
	}

	// Shipped: on_hand and reserved drop together, twice is once.
	for range 2 {
		if err = inTx(func(tx pgx.Tx) error { return f.svc.CommitStockTx(f.ctxA, tx, hold.Ref) }); err != nil {
			t.Fatal(err)
		}
	}
	stock(t, f, v.ID, domain.Stock{Tracked: true, OnHand: 0, Reserved: 0})
	if err = f.svc.ReleaseStock(f.ctxA, hold.Ref); !errors.Is(err, app.ErrReservationState) {
		t.Fatalf("release after commit: %v", err)
	}
	if got := f.events.take(); len(got) != 0 {
		t.Fatalf("commit changed availability: %v", got)
	}

	// Returned: back on hand (and back in stock), twice is once.
	for range 2 {
		if err = inTx(func(tx pgx.Tx) error { return f.svc.RestockTx(f.ctxA, tx, hold.Ref) }); err != nil {
			t.Fatal(err)
		}
	}
	stock(t, f, v.ID, domain.Stock{Tracked: true, OnHand: 2, Reserved: 0})
	if got := f.events.take(); !slices.Equal(got, []string{changed(p.ID, domain.ReasonStock)}) {
		t.Fatalf("back in stock events = %v", got)
	}
	if err = inTx(func(tx pgx.Tx) error { return f.svc.CommitStockTx(f.ctxA, tx, hold.Ref) }); !errors.Is(err, app.ErrReservationState) {
		t.Fatalf("commit after return: %v", err)
	}

	// A released order cannot ship; unknown refs and bad input.
	other := domain.ReserveRequest{Ref: "order-cancelled", Lines: []domain.ReserveLine{{VariantID: v.ID, Qty: 1}}, Hold: true}
	if _, err = f.svc.ReserveStock(f.ctxA, other); err != nil {
		t.Fatal(err)
	}
	if err = inTx(func(tx pgx.Tx) error { return f.svc.ReleaseStockTx(f.ctxA, tx, other.Ref) }); err != nil {
		t.Fatal(err)
	}
	if err = inTx(func(tx pgx.Tx) error { return f.svc.CommitStockTx(f.ctxA, tx, other.Ref) }); !errors.Is(err, app.ErrReservationState) {
		t.Fatalf("commit after release: %v", err)
	}
	if err = inTx(func(tx pgx.Tx) error { return f.svc.CommitStockTx(f.ctxA, tx, "nope") }); !errors.Is(err, app.ErrReservationNotFound) {
		t.Fatalf("unknown ref: %v", err)
	}
	wantKind(t, inTx(func(tx pgx.Tx) error { return f.svc.RestockTx(f.ctxA, tx, "bad ref") }), errs.KindValidation)
	_, err = f.svc.ReserveStock(f.ctxA, domain.ReserveRequest{Ref: "ttl", Lines: hold.Lines, Hold: true, TTL: 1})
	wantKind(t, err, errs.KindValidation)
	stock(t, f, v.ID, domain.Stock{Tracked: true, OnHand: 2, Reserved: 0})
}

// TestCommitUntrackedAndCrossTenant: a line reserved while stock was not
// tracked never touches inventory, and another store cannot commit a
// store's ref (RLS: it does not exist there).
func TestCommitUntrackedAndCrossTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	v := mustCreate(f.ctxA, t, f, single("Free", 0, false)).Variants[0]
	if _, err := f.svc.ReserveStock(f.ctxA, domain.ReserveRequest{Ref: "u", Lines: []domain.ReserveLine{{VariantID: v.ID, Qty: 7}}, Hold: true}); err != nil {
		t.Fatal(err)
	}
	err := postgres.InTenantTx(f.ctxB, f.pool, func(tx pgx.Tx) error { return f.svc.CommitStockTx(f.ctxB, tx, "u") })
	if !errors.Is(err, app.ErrReservationNotFound) {
		t.Fatalf("store B committing A's ref: %v", err)
	}
	for _, step := range []func(pgx.Tx) error{
		func(tx pgx.Tx) error { return f.svc.CommitStockTx(f.ctxA, tx, "u") },
		func(tx pgx.Tx) error { return f.svc.RestockTx(f.ctxA, tx, "u") },
	} {
		if err = postgres.InTenantTx(f.ctxA, f.pool, step); err != nil {
			t.Fatal(err)
		}
	}
	stock(t, f, v.ID, domain.Stock{Tracked: false, OnHand: 0, Reserved: 0})
}

func stock(t *testing.T, f fixture, id ulid.ID, want domain.Stock) {
	t.Helper()
	sv, err := f.svc.GetSellableVariant(f.ctxA, id)
	if err != nil {
		t.Fatal(err)
	}
	if sv.Stock != want {
		t.Fatalf("stock = %+v, want %+v", sv.Stock, want)
	}
}
