package app_test

import (
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway/fake"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
)

// TestSettings (ADR-0036): checkout starts on cash on delivery until the
// merchant picks card, and on card only while a gateway is connected.
func TestSettings(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	evs := testkit.NewEvents()
	svc := app.NewService(app.Deps{DB: f.pool, IDs: f.ids, Events: evs, Gateways: app.Gateways{fake.Name: f.gw}, Sealer: f.sealer(),
		PublicURL: "https://app.example.com", Orders: f.orders, Jobs: f.jobs})

	st, err := svc.Settings(f.ctxA)
	if err != nil || st != (app.Settings{DefaultMethod: domain.DefaultCOD, CardEnabled: true}) {
		t.Fatalf("A's defaults: %+v %v", st, err)
	}
	if first, e := svc.CardDefault(f.ctxA); e != nil || first {
		t.Fatalf("card first before choosing it: %v %v", first, e)
	}
	if st, err = svc.UpdateSettings(f.ctxA, domain.DefaultCard); err != nil || st.DefaultMethod != domain.DefaultCard {
		t.Fatalf("choose card: %+v %v", st, err)
	}
	if first, e := svc.CardDefault(f.ctxA); e != nil || !first {
		t.Fatalf("card first: %v %v", first, e)
	}
	if evs := evs.All(); len(evs) != 1 || evs[0].EventMeta().Type != domain.EventSettingsChanged {
		t.Fatalf("events %+v", evs)
	}

	// B chose card but has no gateway: checkout starts on cash on delivery.
	if _, err = svc.UpdateSettings(f.ctxB, domain.DefaultCard); err != nil {
		t.Fatal(err)
	}
	if first, e := svc.CardDefault(f.ctxB); e != nil || first {
		t.Fatalf("card first without a gateway: %v %v", first, e)
	}
	if st, _ = svc.Settings(f.ctxA); st.DefaultMethod != domain.DefaultCard {
		t.Fatalf("B's choice changed A's: %+v", st)
	}
	if _, err = svc.UpdateSettings(f.ctxA, "wallet"); !isField(err, "default_method", "invalid") {
		t.Fatalf("unknown method: %v", err)
	}
}
