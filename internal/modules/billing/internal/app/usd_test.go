package app_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// gulfStore is a Saudi store selling in riyals.
func (f fixture) gulfStore(t *testing.T) context.Context {
	t.Helper()
	id := tenant.StoreID{ID: f.ids.New()}
	ctx := tenant.WithStore(t.Context(), id)
	if err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO stores (id, slug, name, currency, country, locale, timezone) VALUES ($1, $2, 'Gulf', 'SAR', 'SA', 'ar', 'Asia/Riyadh')`,
			id.ID, "gulf-"+strings.ToLower(id.String()[14:]))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return ctx
}

type usdFee struct {
	currency         string
	base, fee, fixed int64
	orderCurrency    *string
	orderBase, rate  *int64
	status           string
}

func (f fixture) usdFee(ctx context.Context, t *testing.T, order ulid.ID) usdFee {
	t.Helper()
	var r usdFee
	if err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT currency, base_minor, fee_minor, fixed_minor, order_currency, order_base_minor, rate_micros, status
			FROM sell_fees WHERE order_id = $1`, order).Scan(&r.currency, &r.base, &r.fee, &r.fixed, &r.orderCurrency, &r.orderBase, &r.rate, &r.status)
	}); err != nil {
		t.Fatal(err)
	}
	return r
}

// paysRule is the pay-as-you-sell plan's fee rule in currency.
func (f fixture) paysRule(t *testing.T, currency string) domain.FeeRule {
	t.Helper()
	var r domain.FeeRule
	if err := f.pool.QueryRow(t.Context(), `SELECT f.rate_bps, f.fixed_minor, f.min_minor, f.max_minor FROM plan_fees f JOIN plans p ON p.id = f.plan_id
		WHERE p.code = 'pays' AND p.status = 'active' AND f.currency = $1`, currency).Scan(&r.RateBps, &r.FixedMinor, &r.MinMinor, &r.MaxMinor); err != nil {
		t.Fatal(err)
	}
	return r
}

func sar(t *testing.T, minor int64) money.Money {
	t.Helper()
	m, err := money.New(minor, money.SAR)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestDollarBilling: a Saudi merchant is billed in dollars (ADR-0090):
// its offers are in dollars, a delivered order's fee is its riyals
// converted at the console's rate (kept with the fee), and its account
// opens in dollars in its own country. A store whose account was opened
// in riyals before keeps them.
func TestDollarBilling(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	book := &orderBook{orders: map[ulid.ID]app.Order{}}
	jobs := testkit.NewJobs()
	svc := app.NewService(app.Deps{DB: f.pool, Orders: book.get, Jobs: jobs, IDs: f.ids, Now: func() time.Time { return runAt },
		Stores: func(context.Context) (app.StoreInfo, error) {
			return app.StoreInfo{Name: "متجر الرياض", Currency: "SAR", Country: "SA"}, nil
		}})
	riyadh := f.gulfStore(t)
	f.subscribe(riyadh, t, "pays", "active", "")

	offers, err := svc.Offers(riyadh)
	// In dollars, before Saudi Arabia's VAT (seeded at 15%).
	if err != nil || offers.Currency != "USD" || offers.TaxBps != 1500 {
		t.Fatalf("offers = %s %d, %v", offers.Currency, offers.TaxBps, err)
	}
	for _, o := range offers.Plans {
		if o.Kind == domain.KindPays && (o.Fee == nil || o.Fee.Currency != "USD") {
			t.Fatalf("pay as you sell's fee = %+v", o.Fee)
		}
	}
	// The dollar's minimum: $10, or the $15 TestCollectionSettings sets
	// for a moment while it runs in parallel (the settings are one row).
	if b, bErr := svc.StoreBalance(riyadh); bErr != nil || b.Currency != "USD" || (b.MinimumMinor != 1000 && b.MinimumMinor != 1500) || b.Gateway != "stripe" {
		t.Fatalf("balance before an account = %+v, %v", b, bErr)
	}

	order := f.ids.New()
	book.set(order, app.Order{Status: app.OrderDelivered, Base: sar(t, 37500), StatusAt: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)})
	for range 2 { // a retried job
		if err = svc.OrderMoved(riyadh, order); err != nil {
			t.Fatal(err)
		}
	}
	got := f.usdFee(riyadh, t, order)
	want := f.paysRule(t, "USD").Fee(10000)
	if got.currency != "USD" || got.base != 10000 || got.fee != want || got.orderCurrency == nil || *got.orderCurrency != "SAR" ||
		*got.orderBase != 37500 || *got.rate != 3_750_000 || got.status != "due" {
		t.Fatalf("fee = %+v (%v %v %v), want 100.00 USD and %d", got, got.orderCurrency, got.orderBase, got.rate, want)
	}

	if err = svc.InvoiceStore(riyadh, october); err != nil {
		t.Fatal(err)
	}
	var account ulid.ID
	for _, a := range jobs.Args {
		if j, ok := a.(app.InvoiceAccountArgs); ok {
			account = j.AccountID
		}
	}
	var cur, country string
	if err = postgres.InTx(t.Context(), f.pool, func(tx pgx.Tx) error {
		if _, xErr := tx.Exec(t.Context(), `SELECT set_config('app.account_id', $1::uuid::text, true)`, account); xErr != nil {
			return xErr
		}
		return tx.QueryRow(t.Context(), `SELECT currency, country FROM billing_accounts WHERE id = $1`, account).Scan(&cur, &country)
	}); err != nil || cur != "USD" || country != "SA" {
		t.Fatalf("account = %s %s, %v", cur, country, err)
	}

	// An account opened in riyals before the dollar keeps them: its fee
	// is in riyals, at par.
	older := f.gulfStore(t)
	f.subscribe(older, t, "pays", "active", "")
	legacy := f.ids.New()
	if err = postgres.InTenantTx(older, f.pool, func(tx pgx.Tx) error {
		if _, xErr := tx.Exec(older, `SELECT set_config('app.account_id', $1::uuid::text, true)`, legacy); xErr != nil {
			return xErr
		}
		if _, xErr := tx.Exec(older, `INSERT INTO billing_accounts (id, kind, name, currency, country) VALUES ($1, 'store', 'قديم', 'SAR', 'SA')`, legacy); xErr != nil {
			return xErr
		}
		if _, xErr := tx.Exec(older, `INSERT INTO billing_account_stores (account_id, member_store_id) VALUES ($1, app.current_store_id())`, legacy); xErr != nil {
			return xErr
		}
		_, xErr := tx.Exec(older, `UPDATE subscriptions SET account_id = $1`, legacy)
		return xErr
	}); err != nil {
		t.Fatal(err)
	}
	order = f.ids.New()
	book.set(order, app.Order{Status: app.OrderDelivered, Base: sar(t, 37500), StatusAt: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)})
	if err = svc.OrderMoved(older, order); err != nil {
		t.Fatal(err)
	}
	if got = f.usdFee(older, t, order); got.currency != "SAR" || got.base != 37500 || got.orderCurrency != nil || got.fee != f.paysRule(t, "SAR").Fee(37500) {
		t.Fatalf("legacy fee = %+v", got)
	}
	if offers, err = svc.Offers(older); err != nil || offers.Currency != "SAR" {
		t.Fatalf("legacy offers = %s, %v", offers.Currency, err)
	}
}

// TestFXRates: the riyal and the dirham are seeded at their pegs; the
// console sets a rate, merchants' code cannot, and a rate out of bounds
// is refused. The pound only: other tests convert the riyal.
func TestFXRates(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	adminPool, err := pgxpool.New(t.Context(), infra.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adminPool.Close)
	console := app.NewService(app.Deps{DB: adminPool})

	rates, err := f.svc.FXRates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	seeded := map[string]int64{}
	for _, r := range rates {
		seeded[r.Currency] = r.PerUSDMicros
	}
	if seeded["SAR"] != 3_750_000 || seeded["AED"] != 3_672_500 {
		t.Fatalf("rates = %v", seeded)
	}
	pound := domain.FXRate{Currency: "EGP", PerUSDMicros: 48_500_000}
	if err = f.svc.SaveFXRate(t.Context(), pound); err == nil {
		t.Fatal("the merchants' role set a rate")
	}
	for _, bad := range []domain.FXRate{{Currency: "USD", PerUSDMicros: 1}, {Currency: "EGP", PerUSDMicros: 0}} {
		if err = console.SaveFXRate(t.Context(), bad); !errs.Is(err, errs.KindValidation) {
			t.Fatalf("%+v: %v", bad, err)
		}
	}
	if err = console.SaveFXRate(t.Context(), pound); err != nil {
		t.Fatal(err)
	}
	rates, err = console.FXRates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if rate, ok := domain.RateBetween(rates, "EGP", "USD"); !ok || rate != 48_500_000 {
		t.Fatalf("the pound = %d, %v", rate, ok)
	}
}

// TestDollarAgency: an agency outside Egypt is billed in dollars in its
// own country; nothing new is sold in the riyal (ADR-0090).
func TestDollarAgency(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a, err := f.svc.CreateAgency(t.Context(), "وكالة الخليج", "USD", "sa", "agency")
	if err != nil || a.Currency != "USD" || a.Country != "SA" {
		t.Fatalf("agency = %+v, %v", a, err)
	}
	for name, call := range map[string][3]string{
		"the riyal":           {"SAR", "SA", "agency"},
		"the pound abroad":    {"EGP", "SA", "agency"},
		"the dollar in Egypt": {"USD", "EG", "agency"},
		"no country":          {"USD", "", "agency"},
		"not a country":       {"USD", "XX", "agency"},
	} {
		if _, err = f.svc.CreateAgency(t.Context(), "وكالة", call[0], call[1], call[2]); !errs.Is(err, errs.KindValidation) {
			t.Errorf("%s: %v", name, err)
		}
	}
	list, err := f.svc.Agencies(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range list {
		if x.ID == a.ID && x.Country != "SA" {
			t.Fatalf("listed = %+v", x)
		}
	}
}
