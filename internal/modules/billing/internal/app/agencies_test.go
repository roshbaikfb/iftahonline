package app_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// sub reads the store-in-ctx's plan, status, period end and account.
func (d *dunning) sub(ctx context.Context, t *testing.T) (plan ulid.ID, status string, periodEnd *time.Time, account *ulid.ID) {
	t.Helper()
	if err := postgres.InTenantTx(ctx, d.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT plan_id, status, period_end, account_id FROM subscriptions`).Scan(&plan, &status, &periodEnd, &account)
	}); err != nil {
		t.Fatal(err)
	}
	return plan, status, periodEnd, account
}

// TestAgencies: the approved agency rules (ADR-0059). A store joins from
// no account or from its own once that owes nothing, shares the agency's
// period, and leaves onto the free plan.
func TestAgencies(t *testing.T) {
	t.Parallel()
	d := newDunning(t)
	first, second := d.store(t), d.store(t)

	if _, err := d.svc.CreateAgency(t.Context(), "وكالة", "EGP", "pro"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("a subscription plan: %v", err)
	}
	for name, err := range map[string]error{
		"no name":      func() error { _, e := d.svc.CreateAgency(t.Context(), " ", "EGP", "agency"); return e }(),
		"bad currency": func() error { _, e := d.svc.CreateAgency(t.Context(), "وكالة", "USD", "agency"); return e }(),
	} {
		if !errs.Is(err, errs.KindValidation) {
			t.Errorf("%s: %v", name, err)
		}
	}
	a, err := d.svc.CreateAgency(t.Context(), "وكالة النيل", "EGP", "agency")
	if err != nil || a.Country != "EG" || a.PlanCode != "agency" {
		t.Fatalf("agency = %+v, %v", a, err)
	}

	// The first member: no subscription before; the agency's plan from
	// now to the first of next month.
	if err = d.svc.JoinAgency(first, a.ID); err != nil {
		t.Fatal(err)
	}
	_, status, end, account := d.sub(first, t)
	if status != "active" || account == nil || *account != a.ID || !end.Equal(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("first member = %s %v %v", status, end, account)
	}
	if err = d.svc.JoinAgency(first, a.ID); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("join twice: %v", err)
	}

	// The second has its own account with an open invoice: it pays first.
	d.subscribe(second, t, "pro", "active", "cycle, period_start, period_end='monthly', '2026-10-01T00:00:00Z', '2026-11-01T00:00:00Z'")
	own := d.invoiceOf(second, t)
	if err = d.svc.JoinAgency(second, a.ID); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("join with an open invoice: %v", err)
	}
	if err = d.svc.SettleInvoice(t.Context(), own, d.invoiceID(t, own), "transfer"); err != nil {
		t.Fatal(err)
	}
	if err = d.svc.JoinAgency(second, a.ID); err != nil {
		t.Fatal(err)
	}
	firstPlan, _, firstEnd, _ := d.sub(first, t)
	plan, status, end, account := d.sub(second, t)
	if plan != firstPlan || status != "active" || !end.Equal(*firstEnd) || *account != a.ID {
		t.Fatalf("second member = %v %s %v %v; first %v %v", plan, status, end, account, firstPlan, firstEnd)
	}
	// Its paid invoice stayed in its own account.
	if st, _, _ := d.invoice(t, own); st != "paid" {
		t.Fatalf("own invoice = %s", st)
	}
	got, members, err := d.svc.Agency(t.Context(), a.ID)
	if err != nil || got.Stores != 2 || len(members) != 2 {
		t.Fatalf("agency = %+v %v %v", got, members, err)
	}
	list, err := d.svc.Agencies(t.Context())
	if err != nil || !slices.ContainsFunc(list, func(x domain.Agency) bool { return x.ID == a.ID && x.Stores == 2 }) {
		t.Fatalf("agencies = %v, %v", len(list), err)
	}

	// Leaving: the free plan, no account; the agency keeps the other.
	if err = d.svc.LeaveAgency(second); err != nil {
		t.Fatal(err)
	}
	plan, status, end, account = d.sub(second, t)
	if plan == firstPlan || status != "active" || end != nil || account != nil || d.adFree(second, t) {
		t.Fatalf("after leaving = %v %s %v %v", plan, status, end, account)
	}
	if err = d.svc.LeaveAgency(second); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("leave twice: %v", err)
	}
	if _, members, _ = d.svc.Agency(t.Context(), a.ID); len(members) != 1 {
		t.Fatalf("members after leaving = %v", members)
	}
	if _, _, err = d.svc.Agency(t.Context(), d.ids.New()); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("unknown agency: %v", err)
	}
	if err = d.svc.JoinAgency(second, d.ids.New()); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("join an unknown agency: %v", err)
	}
	// Every change published SubscriptionChanged; none held a store.
	n := 0
	for _, ev := range d.events.All() {
		if ev.EventMeta().Type == domain.EventSubscriptionChanged {
			n++
		}
	}
	if n < 3 {
		t.Fatalf("%d SubscriptionChanged, want at least 3 (two joins, one leave)", n)
	}
	for _, ctx := range []context.Context{first, second} {
		if d.holds.of(ctx) {
			id, _ := tenant.FromContext(ctx)
			t.Fatalf("%v is held", id)
		}
	}
}
