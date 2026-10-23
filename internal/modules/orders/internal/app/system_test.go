package app_test

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
)

// TestTransitionSystemTx: a carrier moves an order without a person's
// permission, along the same edges and with the same stock effects; the
// timeline names the carrier; a repeated move is a no-op; another store
// cannot move it.
func TestTransitionSystemTx(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	v := f.product(t, f.storeA, "ساعة", 120000, 5)
	p, err := f.svc.PlaceOrder(f.shopperA, newOrder("", domain.LineInput{VariantID: v, Qty: 2}))
	if err != nil {
		t.Fatal(err)
	}
	id := p.Order.ID
	for _, to := range []domain.Status{domain.StatusConfirmed, domain.StatusReadyToShip} {
		if _, err = f.svc.Transition(f.asRole(f.shopperA, authz.RoleOwner), id, to, "", 0); err != nil {
			t.Fatal(err)
		}
	}
	f.audit.Take()
	f.events.Take()

	// The worker's context: the store, no principal.
	move := func(to domain.Status, actor string) (bool, error) {
		var moved bool
		err := postgres.InTenantTx(f.shopperA, f.pool, func(tx pgx.Tx) (txErr error) {
			moved, txErr = f.svc.TransitionSystemTx(f.shopperA, tx, id, to, actor, "tracking 7000001")
			return txErr
		})
		return moved, err
	}
	if moved, moveErr := move(domain.StatusShipped, "carrier:bosta"); moveErr != nil || !moved {
		t.Fatalf("shipped by the carrier: %v %v", moved, moveErr)
	}
	if on, res := f.stock(f.ctxA, t, v); on != 3 || res != 0 {
		t.Fatalf("stock after the carrier's pickup: %d/%d, want 3/0", on, res)
	}
	if moved, moveErr := move(domain.StatusShipped, "carrier:bosta"); moveErr != nil || moved {
		t.Fatalf("repeated move: %v %v, want a no-op", moved, moveErr)
	}
	if moved, moveErr := move(domain.StatusDelivered, "carrier:bosta"); moveErr != nil || !moved {
		t.Fatalf("delivered: %v %v", moved, moveErr)
	}
	if _, moveErr := move(domain.StatusConfirmed, "carrier:bosta"); !errors.Is(moveErr, domain.ErrInvalidTransition) {
		t.Fatalf("a forbidden move: %v", moveErr)
	}
	for _, bad := range []string{"", "bosta", "Carrier:bosta", "carrier:"} {
		if _, moveErr := move(domain.StatusReturned, bad); moveErr == nil {
			t.Errorf("actor %q accepted", bad)
		}
	}

	o, err := f.svc.Get(f.ctxA, id)
	if err != nil || o.Status != domain.StatusDelivered {
		t.Fatalf("order %+v %v", o, err)
	}
	detail, err := f.svc.GetDetail(f.ctxA, id)
	if err != nil {
		t.Fatal(err)
	}
	last := detail.Events[len(detail.Events)-1]
	if last.Actor != "carrier:bosta" || last.ActorUserID != nil || last.Note != "tracking 7000001" || len(detail.Events) != 5 { // placed, confirmed, ready, shipped, delivered
		t.Fatalf("timeline: %d events, last %+v", len(detail.Events), last)
	}
	if got := f.audit.Take(); len(got) != 2 || got[0] != "order.shipped "+id.String() {
		t.Fatalf("audit %v", got)
	}

	// Store B's worker cannot move store A's order.
	err = postgres.InTenantTx(f.ctxB, f.pool, func(tx pgx.Tx) error {
		_, txErr := f.svc.TransitionSystemTx(f.ctxB, tx, id, domain.StatusReturned, "carrier:bosta", "")
		return txErr
	})
	if !errors.Is(err, app.ErrOrderNotFound) {
		t.Fatalf("cross-tenant move: %v", err)
	}
}
