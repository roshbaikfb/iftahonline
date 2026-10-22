package app_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// TestLifecycleMovesStock walks an order through every stock effect:
// held while pending, shipped (on_hand drops), a failed delivery retried
// (no second commit), delivered, returned (back on hand). Each step
// writes the timeline, an audit entry and an event in its transaction.
func TestLifecycleMovesStock(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	v := f.product(t, f.storeA, "ساعة", 120000, 5)
	p, err := f.svc.PlaceOrder(f.shopperA, newOrder("", domain.LineInput{VariantID: v, Qty: 2}))
	if err != nil {
		t.Fatal(err)
	}
	id := p.Order.ID
	f.audit.Take()
	f.events.Take()

	steps := []struct {
		to               domain.Status
		onHand, reserved int
		role             authz.Role
	}{
		{domain.StatusConfirmed, 5, 2, authz.RoleSupport},
		{domain.StatusReadyToShip, 5, 2, authz.RoleShipping},
		{domain.StatusShipped, 3, 0, authz.RoleShipping},
		{domain.StatusFailedDelivery, 3, 0, authz.RoleShipping},
		{domain.StatusShipped, 3, 0, authz.RoleShipping}, // retried: committed once only
		{domain.StatusDelivered, 3, 0, authz.RoleShipping},
		{domain.StatusReturned, 5, 0, authz.RoleOwner},
	}
	var wantAudit, wantEvents []string
	from := domain.StatusPending
	for i, s := range steps {
		o, moveErr := f.svc.Transition(f.asRole(f.shopperA, s.role), id, s.to, "", 0)
		if moveErr != nil {
			t.Fatalf("step %d to %s: %v", i, s.to, moveErr)
		}
		if o.Status != s.to || o.Version != i+2 {
			t.Fatalf("step %d: %+v", i, o)
		}
		if on, res := f.stock(f.ctxA, t, v); on != s.onHand || res != s.reserved {
			t.Fatalf("after %s: stock %d/%d, want %d/%d", s.to, on, res, s.onHand, s.reserved)
		}
		wantAudit = append(wantAudit, "order."+string(s.to)+" "+id.String())
		wantEvents = append(wantEvents, "orders.order.status_changed "+id.String()+" "+string(from)+"->"+string(s.to))
		from = s.to
	}
	if got := f.audit.Take(); !slices.Equal(got, wantAudit) {
		t.Fatalf("audit %v", got)
	}
	if got := f.events.Take(); !slices.Equal(got, wantEvents) {
		t.Fatalf("events %v", got)
	}
	d, err := f.svc.GetDetail(f.ctxA, id)
	if err != nil || len(d.Events) != len(steps)+1 {
		t.Fatalf("timeline: %d events, %v", len(d.Events), err)
	}
	for i, e := range d.Events[1:] {
		if e.To != steps[i].to || e.ActorUserID == nil {
			t.Fatalf("timeline[%d] = %+v", i+1, e)
		}
	}
	// Returned is final.
	_, err = f.svc.Transition(f.ctxA, id, domain.StatusShipped, "", 0)
	var te *domain.TransitionError
	if !errors.As(err, &te) || te.From != domain.StatusReturned || te.To != domain.StatusShipped {
		t.Fatalf("move after return: %v", err)
	}
}

func TestCancelReleasesStock(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	v := f.product(t, f.storeA, "حقيبة", 80000, 3)
	for _, before := range [][]domain.Status{nil, {domain.StatusConfirmed}, {domain.StatusConfirmed, domain.StatusReadyToShip}} {
		p, err := f.svc.PlaceOrder(f.ctxA, merchantOrder(domain.LineInput{VariantID: v, Qty: 3}))
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range before {
			if _, err = f.svc.Transition(f.ctxA, p.Order.ID, s, "", 0); err != nil {
				t.Fatal(err)
			}
		}
		o, err := f.svc.Transition(f.ctxA, p.Order.ID, domain.StatusCancelled, "  العميل   اعتذر ", 0)
		if err != nil || o.Status != domain.StatusCancelled {
			t.Fatalf("cancel after %v: %v", before, err)
		}
		if on, res := f.stock(f.ctxA, t, v); on != 3 || res != 0 {
			t.Fatalf("after cancelling: %d/%d", on, res)
		}
		d, _ := f.svc.GetDetail(f.ctxA, p.Order.ID)
		if last := d.Events[len(d.Events)-1]; last.Note != "العميل اعتذر" {
			t.Fatalf("note %q", last.Note)
		}
	}
}

func merchantOrder(lines ...domain.LineInput) domain.NewOrder {
	o := newOrder("", lines...)
	o.Source = domain.SourceMerchant
	return o
}

// TestTransitionGuards: forbidden moves, stale versions, roles without
// the step's permission, other stores and bad input change nothing.
func TestTransitionGuards(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	v := f.product(t, f.storeA, "كتاب", 9000, 10)
	p, err := f.svc.PlaceOrder(f.ctxA, merchantOrder(domain.LineInput{VariantID: v, Qty: 1}))
	if err != nil {
		t.Fatal(err)
	}
	id := p.Order.ID
	unchanged := func() {
		t.Helper()
		o, getErr := f.svc.Get(f.ctxA, id)
		if getErr != nil || o.Status != domain.StatusPending || o.Version != 1 {
			t.Fatalf("order changed: %+v %v", o, getErr)
		}
		if _, res := f.stock(f.ctxA, t, v); res != 1 {
			t.Fatalf("stock changed: reserved %d", res)
		}
	}

	_, err = f.svc.Transition(f.ctxA, id, domain.StatusShipped, "", 0)
	if !errors.Is(err, domain.ErrInvalidTransition) || !errs.Is(err, errs.KindConflict) {
		t.Fatalf("pending -> shipped: %v", err)
	}
	if _, err = f.svc.Transition(f.ctxA, id, domain.StatusConfirmed, "", 7); !errors.Is(err, app.ErrVersionConflict) {
		t.Fatalf("stale version: %v", err)
	}
	if _, err = f.svc.Transition(f.asRole(f.shopperA, authz.RoleShipping), id, domain.StatusConfirmed, "", 0); !errs.Is(err, errs.KindForbidden) {
		t.Fatalf("shipping confirms: %v", err)
	}
	if _, err = f.svc.Transition(f.shopperA, id, domain.StatusConfirmed, "", 0); err == nil {
		t.Fatal("a shopper (no principal) moved an order")
	}
	if _, err = f.svc.Transition(f.ctxB, id, domain.StatusConfirmed, "", 0); !errors.Is(err, app.ErrOrderNotFound) {
		t.Fatalf("store B moves A's order: %v", err)
	}
	if _, err = f.svc.Transition(f.ctxA, ulid.NewGenerator().New(), domain.StatusConfirmed, "", 0); !errors.Is(err, app.ErrOrderNotFound) {
		t.Fatalf("unknown order: %v", err)
	}
	if _, err = f.svc.Transition(f.ctxA, id, domain.StatusConfirmed, strings.Repeat("ن", domain.MaxNotesLen+1), 0); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("long note: %v", err)
	}
	unchanged()

	// The right role, with the current version, moves it.
	support := f.asRole(f.shopperA, authz.RoleSupport)
	if o, moveErr := f.svc.Transition(support, id, domain.StatusConfirmed, "", 1); moveErr != nil || o.Version != 2 {
		t.Fatalf("support confirms: %+v %v", o, moveErr)
	}
	// Support cannot ship.
	if _, err = f.svc.Transition(support, id, domain.StatusReadyToShip, "", 0); !errs.Is(err, errs.KindForbidden) {
		t.Fatalf("support packs: %v", err)
	}
}

func TestBulkTransition(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	v := f.product(t, f.storeA, "قلم", 1500, 100)
	var ids []ulid.ID
	for range 3 {
		p, err := f.svc.PlaceOrder(f.ctxA, merchantOrder(domain.LineInput{VariantID: v, Qty: 1}))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.Order.ID)
	}
	if _, err := f.svc.Transition(f.ctxA, ids[1], domain.StatusCancelled, "", 0); err != nil {
		t.Fatal(err)
	}
	foreign, err := f.svc.PlaceOrder(f.ctxB, merchantOrder(domain.LineInput{VariantID: f.product(t, f.storeB, "قلم B", 1500, 5), Qty: 1}))
	if err != nil {
		t.Fatal(err)
	}
	ask := append(slices.Clone(ids), ulid.NewGenerator().New(), foreign.Order.ID)
	res, err := f.svc.BulkTransition(f.ctxA, ask, domain.StatusConfirmed, "تم التأكيد هاتفيًا")
	if err != nil || len(res) != 5 {
		t.Fatalf("bulk: %v %v", res, err)
	}
	if res[0].Err != nil || res[0].Order.Status != domain.StatusConfirmed || res[2].Err != nil {
		t.Fatalf("pending orders: %+v %+v", res[0], res[2])
	}
	if !errors.Is(res[1].Err, domain.ErrInvalidTransition) {
		t.Fatalf("cancelled order: %v", res[1].Err)
	}
	if !errors.Is(res[3].Err, app.ErrOrderNotFound) || !errors.Is(res[4].Err, app.ErrOrderNotFound) {
		t.Fatalf("unknown and foreign: %v / %v", res[3].Err, res[4].Err)
	}
	if o, _ := f.svc.Get(f.ctxB, foreign.Order.ID); o.Status != domain.StatusPending {
		t.Fatalf("store B's order moved: %s", o.Status)
	}
	for _, bad := range [][]ulid.ID{nil, make([]ulid.ID, app.MaxBulk+1), {ids[0], ids[0]}} {
		if _, err = f.svc.BulkTransition(f.ctxA, bad, domain.StatusConfirmed, ""); !errs.Is(err, errs.KindValidation) {
			t.Fatalf("bad bulk of %d: %v", len(bad), err)
		}
	}
}
