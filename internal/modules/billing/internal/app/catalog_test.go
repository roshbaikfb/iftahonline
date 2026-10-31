package app_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/billing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// newCatalog is the catalog on the console's role, as cmd/platform wires
// it. Tests use codes and keys of their own: the seeded plans are shared
// by every parallel test.
func newCatalog(t *testing.T) (*app.Catalog, *ulid.Generator) {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), infra.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ids := ulid.NewGenerator()
	return app.NewCatalog(pool, ids), ids
}

func uniqueKey(ids *ulid.Generator, prefix string) string {
	return prefix + strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return 'a' + (r - '0')
		}
		return r
	}, strings.ToLower(ids.New().String()[16:]))
}

func paidVersion(code string, egp int64) domain.PlanVersion {
	return domain.PlanVersion{
		Plan: domain.Plan{Code: code, Kind: domain.KindSubscription, NameAr: "تجربة", NameEn: "Test", TrialDays: 7, Public: true},
		Prices: []domain.Price{
			{Currency: "EGP", Cycle: domain.Monthly, AmountMinor: egp},
			{Currency: "SAR", Cycle: domain.Monthly, AmountMinor: 1000},
			{Currency: "AED", Cycle: domain.Monthly, AmountMinor: 1000},
		},
		Grants: domain.Grants{"products": {Value: 10}, "coupons": {Value: 1}, "staff": {Unlimited: true}},
	}
}

func TestCatalogFeatures(t *testing.T) {
	t.Parallel()
	c, ids := newCatalog(t)
	key := uniqueKey(ids, "t_")
	f := domain.Feature{Key: key, Kind: domain.FeatureFlag, NameAr: "دردشة", NameEn: "Live chat", Sort: 500}
	if err := c.SaveFeature(t.Context(), f); err != nil {
		t.Fatal(err)
	}
	f.NameEn = "Live chat (beta)"
	if err := c.SaveFeature(t.Context(), f); err != nil {
		t.Fatalf("rename: %v", err)
	}
	all, err := c.Features(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(all, func(x domain.Feature) bool { return x.Key == key })
	if i < 0 || all[i] != f || !slices.ContainsFunc(all, func(x domain.Feature) bool { return x.Key == "products" }) {
		t.Fatalf("catalog = %+v", all)
	}
	f.Kind = domain.FeatureCount
	if err = c.SaveFeature(t.Context(), f); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("kind change: %v", err)
	}
	if err = c.SaveFeature(t.Context(), domain.Feature{Key: "Bad Key"}); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("invalid: %v", err)
	}
}

func TestPublishPlanVersions(t *testing.T) {
	t.Parallel()
	c, ids := newCatalog(t)
	code := uniqueKey(ids, "t_")
	v1, err := c.PublishPlan(t.Context(), paidVersion(code, 10000))
	if err != nil || v1.Version != 1 || !v1.Active || v1.ID.IsZero() {
		t.Fatalf("v1 = %+v, %v", v1, err)
	}
	v2, err := c.PublishPlan(t.Context(), paidVersion(code, 20000))
	if err != nil || v2.Version != 2 {
		t.Fatalf("v2 = %+v, %v", v2, err)
	}
	plans, err := c.Plans(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var mine []domain.PlanVersion
	for _, p := range plans {
		if p.Code == code {
			mine = append(mine, p)
		}
	}
	// Newest first; one version on sale; each with what it sells.
	if len(mine) != 2 || mine[0].Version != 2 || !mine[0].Active || mine[1].Active ||
		egpOf(mine[0]) != 20000 || egpOf(mine[1]) != 10000 ||
		mine[0].Grants["staff"] != (domain.Grant{Unlimited: true}) || mine[0].Grants["products"].Value != 10 {
		t.Fatalf("versions = %+v", mine)
	}
	// The seeded pay-as-you-sell plan lists its fees.
	if i := slices.IndexFunc(plans, func(p domain.PlanVersion) bool { return p.Kind == domain.KindPays && p.Active }); i < 0 || len(plans[i].Fees) != 3 {
		t.Fatalf("pays fees missing: %+v", plans)
	}

	refused := []struct {
		name string
		v    domain.PlanVersion
		kind errs.Kind
	}{
		{"kind change", func() domain.PlanVersion {
			v := paidVersion(code, 1)
			v.Kind = domain.KindAgency
			return v
		}(), errs.KindConflict},
		{"unknown feature", func() domain.PlanVersion {
			v := paidVersion(code, 1)
			v.Grants["no_such_feature"] = domain.Grant{Value: 1}
			return v
		}(), errs.KindValidation},
		{"flag above 1", func() domain.PlanVersion {
			v := paidVersion(code, 1)
			v.Grants["coupons"] = domain.Grant{Value: 2}
			return v
		}(), errs.KindValidation},
		{"a second free plan", domain.PlanVersion{Plan: domain.Plan{Code: uniqueKey(ids, "t_"), Kind: domain.KindFree, NameAr: "م", NameEn: "F"}}, errs.KindConflict},
		{"invalid", domain.PlanVersion{}, errs.KindValidation},
	}
	for _, tt := range refused {
		if _, pubErr := c.PublishPlan(t.Context(), tt.v); !errs.Is(pubErr, tt.kind) {
			t.Errorf("%s: %v", tt.name, pubErr)
		}
	}

	if err = c.RetirePlan(t.Context(), code); err != nil {
		t.Fatal(err)
	}
	if err = c.RetirePlan(t.Context(), code); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("retire twice: %v", err)
	}
	if err = c.RetirePlan(t.Context(), "free"); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("retire the free plan: %v", err)
	}
	// Retired, the code can come back as version 3.
	if v3, err := c.PublishPlan(t.Context(), paidVersion(code, 30000)); err != nil || v3.Version != 3 {
		t.Fatalf("v3 = %+v, %v", v3, err)
	}
}

// TestRenewalIntoTheNewVersion: a store keeps the version it bought until
// its renewal, which charges the newest version's price; paying it moves
// the store to that version (ADR-0056).
func TestRenewalIntoTheNewVersion(t *testing.T) {
	t.Parallel()
	c, ids := newCatalog(t)
	code := uniqueKey(ids, "t_")
	v1, err := c.PublishPlan(t.Context(), paidVersion(code, 10000))
	if err != nil {
		t.Fatal(err)
	}
	d := newDunning(t)
	ctx := d.store(t)
	d.own(ctx, ulid.ID{})
	d.subscribe(ctx, t, code, "active", "cycle, period_start, period_end='monthly', '2026-10-01T00:00:00Z', '2026-11-01T00:00:00Z'")
	v2, err := c.PublishPlan(t.Context(), paidVersion(code, 20000))
	if err != nil {
		t.Fatal(err)
	}
	if got := d.planOf(ctx, t); got != v1.ID {
		t.Fatalf("before renewal: on %v, want v1", got)
	}
	account := d.invoiceOf(ctx, t)
	if got := d.invoices(t, account); len(got) != 1 || !slices.Equal(got[0].lines, []string{"subscription:20000"}) {
		t.Fatalf("renewal invoice = %+v", got)
	}
	if got := d.planOf(ctx, t); got != v1.ID {
		t.Fatalf("billed but unpaid: on %v, want v1", got)
	}
	if err = d.svc.SettleInvoice(t.Context(), account, d.invoiceID(t, account), "transfer-1"); err != nil {
		t.Fatal(err)
	}
	if got := d.planOf(ctx, t); got != v2.ID {
		t.Fatalf("after paying: on %v, want v2 %v", got, v2.ID)
	}
	if g, err := d.svc.Grants(ctx); err != nil || g["products"].Value != 10 {
		t.Fatalf("grants = %+v, %v", g, err)
	}
}

func egpOf(v domain.PlanVersion) int64 {
	for _, p := range v.Prices {
		if p.Currency == "EGP" && p.Cycle == domain.Monthly {
			return p.AmountMinor
		}
	}
	return -1
}

func (d *dunning) planOf(ctx context.Context, t *testing.T) (id ulid.ID) {
	t.Helper()
	if err := postgres.InTenantTx(ctx, d.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT plan_id FROM subscriptions`).Scan(&id)
	}); err != nil {
		t.Fatal(err)
	}
	return id
}
