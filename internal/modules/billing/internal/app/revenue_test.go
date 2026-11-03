package app_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// TestPaying: app.billing_paying lists what each subscription's next
// renewal is charged, and only for subscriptions that pay and renew
// (ADR-0075). Not parallel: it adds founding members, which the founding
// test counts as seats taken, and removes them before it ends.
func TestPaying(t *testing.T) {
	d := newDunning(t)
	owner, err := pgxpool.New(t.Context(), infra.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	period := "cycle, period_start, period_end='monthly', '2026-10-01T00:00:00Z', '2026-11-01T00:00:00Z'"
	idOf := func(ctx context.Context) ulid.ID { return ulid.ID(storeOf(ctx)) }

	monthly, annual, upgrading := d.store(t), d.store(t), d.store(t)
	d.subscribe(monthly, t, "merchant", "active", period)
	d.subscribe(annual, t, "pro", "active", "cycle, period_start, period_end='annual', '2026-10-01T00:00:00Z', '2027-10-01T00:00:00Z'")
	d.subscribe(upgrading, t, "merchant", "active",
		"cycle, period_start, period_end, next_cycle, next_plan_id='monthly', '2026-10-01T00:00:00Z', '2026-11-01T00:00:00Z', 'annual', (SELECT id FROM plans WHERE code = 'pro' AND status = 'active')")

	trial, pastDue, cancelling, free := d.store(t), d.store(t), d.store(t), d.store(t)
	d.subscribe(trial, t, "merchant", "trialing", "trial_ends_at='2026-11-20T00:00:00Z'")
	d.subscribe(pastDue, t, "merchant", "past_due", "cycle, period_start, period_end, grace_until='monthly', '2026-10-01T00:00:00Z', '2026-11-01T00:00:00Z', '2026-11-08T00:00:00Z'")
	d.subscribe(cancelling, t, "merchant", "active", "cycle, period_start, period_end, cancel_at_period_end='monthly', '2026-10-01T00:00:00Z', '2026-11-01T00:00:00Z', true")
	d.subscribe(free, t, "free", "active", "")

	member, former := d.store(t), d.store(t)
	d.subscribe(member, t, "merchant", "active", period)
	d.subscribe(former, t, "merchant", "active", period)
	join := func(ctx context.Context, seat int, lost string) {
		t.Helper()
		if _, joinErr := owner.Exec(t.Context(), `INSERT INTO founding_members (store_id, owner_id, seat, discount_bps, currency, plan_code, cycle, locked_minor, lost_at, lost_reason)
			VALUES ($1, $2, $3, 2000, 'EGP', 'merchant', 'monthly', 24900, `+lost+`)`, idOf(ctx), d.ids.New(), seat); joinErr != nil {
			t.Fatal(joinErr)
		}
	}
	join(member, 9901, "NULL, NULL")
	join(former, 9902, "'2026-10-15T00:00:00Z', 'cancelled'")
	t.Cleanup(func() {
		if _, delErr := owner.Exec(context.Background(), `DELETE FROM founding_members WHERE store_id = ANY($1)`, []ulid.ID{idOf(member), idOf(former)}); delErr != nil {
			t.Error(delErr)
		}
	})

	agency, err := d.svc.CreateAgency(t.Context(), "وكالة الإيراد", "EGP", "agency")
	if err != nil {
		t.Fatal(err)
	}
	first, second := d.store(t), d.store(t)
	for _, ctx := range []context.Context{first, second} {
		if err = d.svc.JoinAgency(ctx, agency.ID); err != nil {
			t.Fatal(err)
		}
	}

	var rows []domain.Paying
	if err = postgres.InTx(t.Context(), d.pool, func(tx pgx.Tx) (txErr error) {
		rows, txErr = store.Paying(t.Context(), tx)
		return txErr
	}); err != nil {
		t.Fatal(err)
	}
	// Other tests share the database: keep this test's rows.
	got := map[ulid.ID][]domain.Paying{}
	for _, r := range rows {
		got[r.Account] = append(got[r.Account], r)
	}
	one := func(name string, account ulid.ID) domain.Paying {
		t.Helper()
		if len(got[account]) != 1 {
			t.Fatalf("%s: %d rows %+v", name, len(got[account]), got[account])
		}
		return got[account][0]
	}

	for _, tt := range []struct {
		name    string
		account ulid.ID
		code    string
		cycle   domain.Cycle
		amount  int64
	}{
		{"monthly", idOf(monthly), "merchant", domain.Monthly, 29900},
		{"annual", idOf(annual), "pro", domain.Annual, 599000},
		{"renews into its next plan and cycle", idOf(upgrading), "pro", domain.Annual, 599000},
		{"a former founding member pays the price", idOf(former), "merchant", domain.Monthly, 29900},
	} {
		p := one(tt.name, tt.account)
		if p.Kind != domain.KindSubscription || p.Currency != "EGP" || p.PlanCode != tt.code || p.Cycle != tt.cycle || p.AmountMinor != tt.amount || p.Founding != nil {
			t.Errorf("%s = %+v", tt.name, p)
		}
	}
	m := one("founding member", idOf(member))
	if f := m.Founding; f == nil || f.DiscountBps != 2000 || f.LockedMinor != 24900 || f.PlanCode != "merchant" || f.Cycle != domain.Monthly || f.Currency != "EGP" || m.AmountMinor != 29900 {
		t.Errorf("founding member = %+v, %+v", m, m.Founding)
	}
	if a := got[agency.ID]; len(a) != 2 || a[0].Kind != domain.KindAgency || a[0].AmountMinor != 399900 || a[0].Founding != nil {
		t.Errorf("agency = %+v", a)
	}
	for name, ctx := range map[string]context.Context{"trial": trial, "past due": pastDue, "cancelling": cancelling, "free": free} {
		if len(got[idOf(ctx)]) != 0 {
			t.Errorf("%s pays: %+v", name, got[idOf(ctx)])
		}
	}

	// The console's sum: this test's stores in it, the agency once.
	mine := []domain.Paying{}
	for _, ctx := range []context.Context{monthly, annual, upgrading, member, former} {
		mine = append(mine, got[idOf(ctx)]...)
	}
	mine = append(mine, got[agency.ID]...)
	want := int64(29900 + 49917 + 49917 + 29900 + 399900 + 19920) // annuals over 12; the member: 24900 − 20%
	if r := domain.MRR(mine); len(r) != 1 || r[0].MRRMinor != want || r[0].Accounts != 6 {
		t.Errorf("MRR = %+v; want %d from 6 accounts", r, want)
	}
	all, err := d.svc.Revenue(t.Context())
	if err != nil || len(all) == 0 || all[0].Currency != "EGP" || all[0].MRRMinor < want {
		t.Fatalf("Revenue = %+v, %v", all, err)
	}
}
