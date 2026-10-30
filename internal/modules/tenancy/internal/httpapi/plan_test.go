package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
)

// TestDomainsWithinThePlan: past the plan's custom domains, addDomain is
// a 403 with code plan_limit, which the dashboard turns into an upgrade
// (ADR-0045).
func TestDomainsWithinThePlan(t *testing.T) {
	t.Parallel()
	pool, err := pgxpool.New(t.Context(), infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ent, err := entitlements.New(entitlements.Deps{Pool: pool, Source: entitlements.Fixed{entitlements.CustomDomains: {Value: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	f := newFixtureWith(t, ent.Entitlements())
	if code, body := f.do(t, req(t, http.MethodPost, storePath(f.storeA, "/domains"), tokOwnerA, `{"host":"one-plan.example.com"}`)); code != http.StatusCreated {
		t.Fatalf("the first domain = %d %v", code, body)
	}
	code, body := f.do(t, req(t, http.MethodPost, storePath(f.storeA, "/domains"), tokOwnerA, `{"host":"two-plan.example.com"}`))
	if code != http.StatusForbidden || body["code"] != entitlements.PlanLimitCode {
		t.Fatalf("past the plan = %d %v", code, body)
	}
}
