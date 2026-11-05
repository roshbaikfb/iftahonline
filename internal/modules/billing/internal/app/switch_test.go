package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// TestSwitchToPays is the approved offer (ADR-0087): an unpaid store
// moves to pay as you sell during its grace, its renewal invoice voided,
// active and open again, paying its fees the way it chose; then it may
// change the way. A held store may not switch. Not parallel: it turns
// the platform's collection settings on, and parallel tests wait for it.
func TestSwitchToPays(t *testing.T) {
	adminPool, err := pgxpool.New(t.Context(), infra.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adminPool.Close)
	console := app.NewService(app.Deps{DB: adminPool})
	if err = console.SaveCollectionSettings(t.Context(), domain.CollectionSettings{Prepaid: true, Card: true, Minimums: approvedMinimums}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if rErr := console.SaveCollectionSettings(context.Background(), domain.CollectionSettings{Minimums: approvedMinimums}); rErr != nil {
			t.Error(rErr)
		}
	})

	d := newDunning(t)
	ctx := d.store(t)
	d.subscribe(ctx, t, "pro", "active", "cycle, period_start, period_end='monthly', '2026-10-01T00:00:00Z', '2026-11-01T00:00:00Z'")
	account := d.invoiceOf(ctx, t)
	d.own(ctx, account)
	if err = d.svc.SavePaymentMethod(t.Context(), account, "fake", "decline-card", "Visa •••• 2346"); err != nil {
		t.Fatal(err)
	}
	if n := d.tick(t, runAt.Add(time.Hour)); len(n) != 1 {
		t.Fatalf("day 0 notices = %v", n)
	}

	// Past due: the offer is there, with the fee and the ways offered.
	sum, err := d.svc.Summary(ctx)
	if err != nil || sum.Status != domain.PastDue || sum.Switch == nil || sum.Switch.RateBps <= 0 || len(sum.Switch.Offered) != 2 || sum.Agency {
		t.Fatalf("offer = %+v, %v", sum, err)
	}
	if err = d.svc.SwitchToPays(ctx, "cash"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("an unknown way: %v", err)
	}
	if err = d.svc.SwitchToPays(ctx, domain.CollectBalance); err != nil {
		t.Fatal(err)
	}
	st, grace, _, billed := d.state(ctx, t)
	if st != "active" || grace != nil || billed || d.holds.of(ctx) {
		t.Fatalf("after the switch: %s, grace %v, renewal billed %v", st, grace, billed)
	}
	if s, _, _ := d.invoice(t, account); s != "void" {
		t.Fatalf("the renewal invoice is %s", s)
	}
	if sum, err = d.svc.Summary(ctx); err != nil || sum.Switch != nil || sum.Collection != domain.CollectBalance || len(sum.OpenInvoices) != 0 {
		t.Fatalf("after = %+v, %v", sum, err)
	}
	// Nothing is charged any more, and the store cannot switch twice.
	before := len(d.gw.charges)
	if d.tick(t, runAt.Add(48*time.Hour)); len(d.gw.charges) != before {
		t.Fatal("a voided invoice was charged")
	}
	if err = d.svc.SwitchToPays(ctx, domain.CollectBalance); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("switch twice: %v", err)
	}

	// It changes the way: to the card it saved.
	if err = d.svc.SetCollection(ctx, domain.CollectCard); err != nil {
		t.Fatal(err)
	}
	if sum, _ = d.svc.Summary(ctx); sum.Collection != domain.CollectCard {
		t.Fatalf("collection = %q", sum.Collection)
	}

	// A held store may not switch; a store not on pay as you sell may not
	// choose a way; the card needs one saved.
	held := d.store(t)
	d.subscribe(held, t, "pro", "suspended",
		"cycle, suspended_at, held_at='monthly', '2026-09-01T00:00:00Z', '2026-10-01T00:00:00Z'")
	if err = d.svc.SwitchToPays(held, domain.CollectBalance); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("held: %v", err)
	}
	other := d.store(t)
	d.subscribe(other, t, "pro", "active", "")
	if err = d.svc.SetCollection(other, domain.CollectBalance); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("not on pays: %v", err)
	}
	pays := d.store(t)
	d.subscribe(pays, t, "pays", "active", "")
	if err = d.svc.SetCollection(pays, domain.CollectCard); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("no card: %v", err)
	}
	if err = d.svc.SetCollection(pays, domain.CollectBalance); err != nil {
		t.Fatal(err)
	}
}

// TestLeavingTheBalanceWaitsForItsInvoice: fees taken from the balance
// keep the account on it until the month's invoice takes their tax.
func TestLeavingTheBalanceWaitsForItsInvoice(t *testing.T) {
	t.Parallel()
	p := newPrepaid(t)
	ctx := p.store(t)
	p.subscribe(ctx, t, "pays", "active", "")
	credit, _ := domain.ParseCredit(30000, "transfer", "CIB 4")
	if _, err := p.svc.CreditBalance(ctx, credit, p.ids.New()); err != nil {
		t.Fatal(err)
	}
	account := p.invoiceAccount(ctx, t)
	p.choose(ctx, t, account)
	if err := p.svc.SavePaymentMethod(t.Context(), account, "fake", "good-card", "Visa •••• 4242"); err != nil {
		t.Fatal(err)
	}
	p.move(ctx, t, p.ids.New(), app.OrderDelivered, 100000, time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC))
	if code := codeOf(p.svc.SetCollection(ctx, domain.CollectCard)); code != "prepaid_pending" {
		t.Fatalf("with prepaid fees pending: %q", code)
	}
	if err := p.svc.InvoiceAccount(t.Context(), account, october); err != nil {
		t.Fatal(err)
	}
	// Invoiced: the fees no longer hold it; the card is then refused only
	// because the platform does not offer it (the defaults).
	if code := codeOf(p.svc.SetCollection(ctx, domain.CollectCard)); code != "collection_not_offered" {
		t.Fatalf("after the invoice: %q", code)
	}
}

func codeOf(err error) string {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}
