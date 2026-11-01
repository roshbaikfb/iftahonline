package app_test

import (
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// TestOutcomeReason (ADR-0065): a person gives why an order did not reach
// its customer on the moves that take one; the next move clears it; a
// system may record unconfirmed; bad reasons change nothing.
func TestOutcomeReason(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	v := f.product(t, f.storeA, "ساعة", 120000, 9)
	// Each order has its own quantity: the same lines from the same phone
	// would be the first order again (the duplicate window).
	place := func(qty int) domain.Order {
		t.Helper()
		p, err := f.svc.PlaceOrder(f.shopperA, newOrder("", domain.LineInput{VariantID: v, Qty: qty}))
		if err != nil {
			t.Fatal(err)
		}
		return p.Order
	}
	walk := func(o domain.Order, to ...domain.Status) {
		t.Helper()
		for _, s := range to {
			if _, err := f.svc.Transition(f.ctxA, o.ID, s, "", 0); err != nil {
				t.Fatal(err)
			}
		}
	}

	o := place(1)
	walk(o, domain.StatusConfirmed, domain.StatusReadyToShip, domain.StatusShipped)
	got, err := f.svc.TransitionWithReason(f.ctxA, o.ID, domain.StatusFailedDelivery, "", "no_answer", 0)
	if err != nil || got.OutcomeReason != domain.ReasonNoAnswer {
		t.Fatalf("failed delivery: %q, %v", got.OutcomeReason, err)
	}
	if read, _ := f.svc.Get(f.ctxA, o.ID); read.OutcomeReason != domain.ReasonNoAnswer {
		t.Fatalf("stored reason = %q", read.OutcomeReason)
	}
	// A second attempt delivered it: the reason goes.
	walk(o, domain.StatusShipped)
	if read, _ := f.svc.Get(f.ctxA, o.ID); read.OutcomeReason != domain.ReasonNone {
		t.Fatalf("reason after the next move = %q", read.OutcomeReason)
	}

	// A reason on a move that takes none, or an unknown one, moves nothing.
	p := place(2)
	for _, bad := range []string{"refused", "unconfirmed", "lost"} {
		if _, err = f.svc.TransitionWithReason(f.ctxA, p.ID, domain.StatusConfirmed, "", bad, 0); !errs.Is(err, errs.KindValidation) {
			t.Fatalf("reason %q on confirm: %v", bad, err)
		}
	}
	if _, err = f.svc.BulkTransition(f.ctxA, []ulid.ID{p.ID}, domain.StatusConfirmed, "", "no_answer"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("bulk confirm with a reason: %v", err)
	}
	if read, _ := f.svc.Get(f.ctxA, p.ID); read.Status != domain.StatusPending {
		t.Fatalf("status after refused moves = %s", read.Status)
	}
	res, err := f.svc.BulkTransition(f.ctxA, []ulid.ID{p.ID}, domain.StatusCancelled, "", "refused")
	if err != nil || res[0].Err != nil || res[0].Order.OutcomeReason != domain.ReasonRefused {
		t.Fatalf("bulk cancel refused: %+v, %v", res, err)
	}

	// The confirmation's deadline cancels as unconfirmed.
	c := place(3)
	sys := func(to domain.Status, r domain.OutcomeReason) error {
		return postgres.InTenantTx(f.shopperA, f.pool, func(tx pgx.Tx) error {
			_, moveErr := f.svc.TransitionSystemReasonTx(f.shopperA, tx, c.ID, to, "system:auto_cancel", "", r)
			return moveErr
		})
	}
	if err = sys(domain.StatusConfirmed, domain.ReasonUnconfirmed); err == nil {
		t.Fatal("a system reason on confirm must fail")
	}
	if err = sys(domain.StatusCancelled, domain.OutcomeReason("lost")); err == nil {
		t.Fatal("an unknown system reason must fail")
	}
	if err = sys(domain.StatusCancelled, domain.ReasonUnconfirmed); err != nil {
		t.Fatal(err)
	}
	if read, _ := f.svc.Get(f.ctxA, c.ID); read.Status != domain.StatusCancelled || read.OutcomeReason != domain.ReasonUnconfirmed {
		t.Fatalf("auto-cancelled: %s %q", read.Status, read.OutcomeReason)
	}
}
