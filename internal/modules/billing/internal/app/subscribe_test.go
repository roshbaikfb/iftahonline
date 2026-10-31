package app_test

import (
	"context"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// subscribeGateway is checkoutGateway with Paymob orders of their own:
// the shared database keeps orders unique across parallel tests.
type subscribeGateway struct{ *checkoutGateway }

var subscribeOrders atomic.Int64

func (g subscribeGateway) Checkout(ctx context.Context, c app.CheckoutRequest) (app.CheckoutPage, error) {
	page, err := g.checkoutGateway.Checkout(ctx, c)
	page.ProviderOrderID = strconv.FormatInt(900000000+subscribeOrders.Add(1), 10)
	return page, err
}

// subscribing is a dunning fixture whose service has a checkout page.
func subscribing(t *testing.T) (*dunning, *checkoutGateway) {
	t.Helper()
	d := newDunning(t)
	gw := &checkoutGateway{fakeGateway: d.gw, paid: map[string]bool{}}
	d.svc = app.NewService(app.Deps{DB: d.pool, Jobs: d.jobs, IDs: d.ids, Events: d.events, Now: func() time.Time { return *d.clock },
		Stores: func(context.Context) (app.StoreInfo, error) {
			return app.StoreInfo{Name: "متجر", Currency: "EGP"}, nil
		},
		Gateways: map[string]app.Gateway{"paymob": subscribeGateway{gw}, "fake": d.gw}, Sealer: plainSealer{}, Holds: d.holds.set,
		Notifier: func(context.Context, app.Notice) error { return nil }, PublicURL: "https://api.example.com/", DashboardURL: "https://app.example.com"})
	return d, gw
}

// subscribeInvoices are the store's account's first-period invoices:
// status, plan code and whether dunning would charge them.
func (d *dunning) subscribeInvoices(ctx context.Context, t *testing.T) []string {
	t.Helper()
	var out []string
	if err := postgres.InTenantTx(ctx, d.pool, func(tx pgx.Tx) error {
		var account ulid.ID
		if err := tx.QueryRow(ctx, `SELECT account_id FROM subscriptions`).Scan(&account); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.account_id', $1::uuid::text, true)`, account); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT i.status || ':' || p.code || ':' || i.cycle || ':' || (i.next_attempt_at IS NULL)::text
			FROM invoices i JOIN plans p ON p.id = i.plan_id WHERE i.purpose = 'subscribe' ORDER BY i.issued_at, i.number`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s string
			if err = rows.Scan(&s); err != nil {
				return err
			}
			out = append(out, s)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestChoosingAPlan walks the approved rules end to end (ADR-0060): a
// trial, pay as you sell at once, a paid plan paid first and started
// then, a scheduled change and its undoing, a cancellation.
func TestChoosingAPlan(t *testing.T) {
	t.Parallel()
	d, gw := subscribing(t)
	ctx := d.store(t)
	d.own(ctx, ulid.ID{})

	offers, err := d.svc.Offers(ctx)
	if err != nil || offers.Currency != "EGP" {
		t.Fatalf("offers = %+v, %v", offers, err)
	}
	byCode := map[string]app.Offer{}
	for _, o := range offers.Plans {
		byCode[o.Code] = o
	}
	if _, agency := byCode["agency"]; agency || !byCode["free"].Current || byCode["pro"].Monthly == nil || *byCode["pro"].Monthly != 59900 ||
		*byCode["pro"].Annual != 599000 || byCode["pays"].Fee == nil || byCode["pays"].Fee.RateBps != 250 || !byCode["pro"].TrialAvailable ||
		byCode["pays"].TrialAvailable || len(offers.Features) < 10 {
		t.Fatalf("offers by code = %+v", byCode)
	}

	// A trial, once.
	if err = d.svc.StartTrial(ctx, "pro"); err != nil {
		t.Fatal(err)
	}
	if st, _, _, _ := d.state(ctx, t); st != "trialing" {
		t.Fatalf("after the trial started: %s", st)
	}
	if err = d.svc.StartTrial(ctx, "brand"); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("a second trial: %v", err)
	}

	// Pay as you sell: at once.
	out, err := d.svc.Choose(ctx, "pays", "", app.Payer{})
	if err != nil || out.Action != domain.ActApply || out.CheckoutURL != "" {
		t.Fatalf("pays = %+v, %v", out, err)
	}
	if sum, _ := d.svc.Summary(ctx); sum.PlanNameEn != "Pay as you sell" || sum.Status != "active" {
		t.Fatalf("on pays: %+v", sum)
	}

	// A paid plan: its first period is paid on the gateway's page first.
	out, err = d.svc.Choose(ctx, "merchant", domain.Monthly, app.Payer{})
	if err != nil || out.Action != domain.ActPay || !strings.HasPrefix(out.CheckoutURL, "https://pay.example.com/bc_") {
		t.Fatalf("merchant = %+v, %v", out, err)
	}
	out, err = d.svc.Choose(ctx, "pro", domain.Annual, app.Payer{})
	if err != nil || out.Action != domain.ActPay || len(gw.pages) != 2 || gw.pages[1].AmountMinor != 599000+83860 {
		t.Fatalf("pro annual = %+v %+v, %v", out, gw.pages, err)
	}
	// The first choice's invoice is void; the open one is never charged.
	if got := d.subscribeInvoices(ctx, t); strings.Join(got, ",") != "void:merchant:monthly:true,open:pro:annual:true" {
		t.Fatalf("subscribe invoices = %v", got)
	}
	if sum, _ := d.svc.Summary(ctx); sum.PlanNameEn != "Pay as you sell" {
		t.Fatalf("unpaid, the store stays: %+v", sum)
	}
	account := d.accountOf(ctx, t)
	d.own(ctx, account)
	d.tick(t, runAt.Add(time.Hour))
	if _, attempts, _ := d.invoice(t, account); attempts != 0 {
		t.Fatalf("dunning charged a first period: %d attempts", attempts)
	}

	// Paid: the year starts now.
	paidAt := *d.clock
	if err = d.svc.SettleInvoice(t.Context(), account, d.openInvoice(t, account), "transfer"); err != nil {
		t.Fatal(err)
	}
	st, _, end, _ := d.state(ctx, t)
	if sum, _ := d.svc.Summary(ctx); st != "active" || sum.PlanNameEn != "Pro" || sum.Cycle != domain.Annual || !end.Equal(paidAt.AddDate(1, 0, 0)) {
		t.Fatalf("after paying: %s %v %+v", st, end, sum)
	}

	// During the paid period: a change waits for the renewal, and can be
	// taken back.
	if out, err = d.svc.Choose(ctx, "brand", domain.Monthly, app.Payer{}); err != nil || out.Action != domain.ActSchedule {
		t.Fatalf("brand = %+v, %v", out, err)
	}
	if sum, _ := d.svc.Summary(ctx); sum.PlanNameEn != "Pro" || sum.NextPlanNameEn != "Brand" || sum.NextCycle != domain.Monthly {
		t.Fatalf("scheduled: %+v", sum)
	}
	if err = d.svc.ResumeRenewal(ctx); err != nil {
		t.Fatal(err)
	}
	if sum, _ := d.svc.Summary(ctx); sum.NextPlanNameEn != "" || sum.NextCycle != "" {
		t.Fatalf("taken back: %+v", sum)
	}
	if err = d.svc.ResumeRenewal(ctx); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("nothing to take back: %v", err)
	}
	if err = d.svc.CancelRenewal(ctx); err != nil {
		t.Fatal(err)
	}
	if sum, _ := d.svc.Summary(ctx); !sum.CancelAtPeriodEnd {
		t.Fatalf("cancelled: %+v", sum)
	}

	for name, err := range map[string]error{
		"an unknown plan": func() error { _, e := d.svc.Choose(ctx, "gold", domain.Monthly, app.Payer{}); return e }(),
		"an agency plan":  func() error { _, e := d.svc.Choose(ctx, "agency", domain.Monthly, app.Payer{}); return e }(),
		"no cycle":        func() error { _, e := d.svc.Choose(ctx, "brand", "", app.Payer{}); return e }(),
	} {
		if !errs.Is(err, errs.KindNotFound) && !errs.Is(err, errs.KindConflict) && !errs.Is(err, errs.KindValidation) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func (d *dunning) accountOf(ctx context.Context, t *testing.T) (id ulid.ID) {
	t.Helper()
	if err := postgres.InTenantTx(ctx, d.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT account_id FROM subscriptions`).Scan(&id)
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

// openInvoice is the account's one open invoice.
func (d *dunning) openInvoice(t *testing.T, account ulid.ID) (id ulid.ID) {
	t.Helper()
	if err := postgres.InTx(t.Context(), d.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(t.Context(), `SELECT set_config('app.account_id', $1::uuid::text, true)`, account); err != nil {
			return err
		}
		return tx.QueryRow(t.Context(), `SELECT id FROM invoices WHERE status = 'open'`).Scan(&id)
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestLeavingAPaidPlanForPaysAtTheEnd: pay as you sell, chosen during a
// paid period, starts when the period ends; then the free plan applies
// at once (ADR-0060).
func TestLeavingAPaidPlanForPaysAtTheEnd(t *testing.T) {
	t.Parallel()
	d, _ := subscribing(t)
	ctx := d.store(t)
	d.subscribe(ctx, t, "pro", "active", "cycle, period_start, period_end='monthly', '2026-10-01T00:00:00Z', '2026-11-01T00:00:00Z'")
	out, err := d.svc.Choose(ctx, "pays", "", app.Payer{})
	if err != nil || out.Action != domain.ActSchedule {
		t.Fatalf("pays = %+v, %v", out, err)
	}
	if sum, _ := d.svc.Summary(ctx); !sum.CancelAtPeriodEnd || sum.NextPlanNameEn != "Pay as you sell" || sum.PlanNameEn != "Pro" {
		t.Fatalf("scheduled: %+v", sum)
	}
	// The period ended (the clock is past it): the store moves to pays.
	if err = d.svc.EndPeriod(ctx); err != nil {
		t.Fatal(err)
	}
	if sum, _ := d.svc.Summary(ctx); sum.PlanNameEn != "Pay as you sell" || sum.Status != "active" || sum.CancelAtPeriodEnd {
		t.Fatalf("after the period: %+v", sum)
	}
	offers, err := d.svc.Offers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range offers.Plans {
		if o.Current != (o.Code == "pays") || o.TrialAvailable {
			t.Fatalf("offer %s: current %v trial %v", o.Code, o.Current, o.TrialAvailable)
		}
	}
	if out, err = d.svc.Choose(ctx, "free", "", app.Payer{}); err != nil || out.Action != domain.ActApply {
		t.Fatalf("free = %+v, %v", out, err)
	}
	if sum, _ := d.svc.Summary(ctx); sum.PlanNameEn != "Free" {
		t.Fatalf("on free: %+v", sum)
	}
	if err = d.svc.CancelRenewal(ctx); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("cancel on free: %v", err)
	}
}

// TestSubscribingFromAnInvoicedStore: a pay-as-you-sell store already has
// its account; its first period's invoice goes there, and a cycle the
// plan does not sell is refused.
func TestSubscribingFromAnInvoicedStore(t *testing.T) {
	t.Parallel()
	c, ids := newCatalog(t)
	code := uniqueKey(ids, "t_")
	monthlyOnly := paidVersion(code, 10000)
	if _, err := c.PublishPlan(t.Context(), monthlyOnly); err != nil {
		t.Fatal(err)
	}
	d, gw := subscribing(t)
	ctx := d.store(t)
	d.subscribe(ctx, t, "pays", "active", "")
	d.fee(ctx, t, october, 900, "due")
	account := d.invoiceOf(ctx, t)
	if err := d.svc.SettleInvoice(t.Context(), account, d.invoiceID(t, account), "transfer"); err != nil {
		t.Fatal(err)
	}

	if _, err := d.svc.Choose(ctx, code, domain.Annual, app.Payer{}); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("a cycle not sold: %v", err)
	}
	out, err := d.svc.Choose(ctx, code, domain.Monthly, app.Payer{})
	if err != nil || out.Action != domain.ActPay || len(gw.pages) != 1 || gw.pages[0].AmountMinor != 10000+1400 {
		t.Fatalf("subscribe = %+v %+v, %v", out, gw.pages, err)
	}
	if got := d.accountOf(ctx, t); got != account {
		t.Fatalf("a second account: %v, had %v", got, account)
	}
	if err = d.svc.SettleInvoice(t.Context(), account, d.openInvoice(t, account), "transfer-2"); err != nil {
		t.Fatal(err)
	}
	if sum, _ := d.svc.Summary(ctx); sum.Status != "active" || sum.Cycle != domain.Monthly || sum.PlanNameEn != "Test" {
		t.Fatalf("after paying: %+v", sum)
	}
}
