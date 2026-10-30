package app_test

import (
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
)

// TestOrdersPastThePlan: the plan's monthly orders are a soft limit. An
// order past it is placed like any other; the month's count goes on, and
// the merchant is told once when the limit is reached (ADR-0045).
func TestOrdersPastThePlan(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	events := testkit.NewEvents()
	ent, err := entitlements.New(entitlements.Deps{Pool: f.pool, Source: entitlements.Fixed{entitlements.OrdersPerMonth: {Value: 2}}, Events: events})
	if err != nil {
		t.Fatal(err)
	}
	f.env.Plan = ent.Entitlements()
	svc := f.env.Service(f.pool)
	tee := f.product(t, f.storeA, "تيشيرت", 15000, 10)

	for i, key := range []string{"p-1", "p-2", "p-3"} {
		o := newOrder(key, domain.LineInput{VariantID: tee, Qty: 1})
		o.Phone = []string{"01012345671", "01012345672", "01012345673"}[i]
		if _, err = svc.PlaceOrder(f.shopperA, o); err != nil {
			t.Fatalf("order %d: %v", i+1, err)
		}
	}
	// A replayed key is the same order: it is not counted again.
	if p, rErr := svc.PlaceOrder(f.shopperA, func() domain.NewOrder {
		o := newOrder("p-3", domain.LineInput{VariantID: tee, Qty: 1})
		o.Phone = "01012345673"
		return o
	}()); rErr != nil || !p.Replayed {
		t.Fatalf("replay = %+v, %v", p, rErr)
	}
	u, err := ent.Entitlements().Usage(f.shopperA, entitlements.OrdersPerMonth)
	if err != nil || u.Used != 3 || u.Limit != 2 {
		t.Fatalf("usage = %+v, %v", u, err)
	}
	all := events.All()
	if len(all) != 1 {
		t.Fatalf("%d events, want 1", len(all))
	}
	if ev, ok := all[0].(entitlements.UsageCrossed); !ok || ev.Threshold != entitlements.Reached || ev.Used != 2 {
		t.Fatalf("event = %+v", all[0])
	}
}
