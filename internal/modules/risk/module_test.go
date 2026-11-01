package risk_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/risk"
	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/risktest"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

func TestNew(t *testing.T) {
	t.Parallel()
	pool, err := pgxpool.New(t.Context(), infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ords := risktest.NewOrders(ulid.NewGenerator(), time.Now())
	key := []byte(strings.Repeat("k", 32))
	for name, d := range map[string]risk.Deps{
		"no pool":   {Orders: ords, PhoneKey: key, Jobs: testkit.NewJobs()},
		"no orders": {Pool: pool, PhoneKey: key, Jobs: testkit.NewJobs()},
		"no jobs":   {Pool: pool, Orders: ords, PhoneKey: key},
		"short key": {Pool: pool, Orders: ords, PhoneKey: key[:31], Jobs: testkit.NewJobs()},
	} {
		if _, err = risk.New(d); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// TestLearnHandler: the orders module's events, as the bus delivers
// them, teach the module; an event about an order gone teaches nothing
// and is not retried.
func TestLearnHandler(t *testing.T) {
	t.Parallel()
	pool, err := pgxpool.New(t.Context(), infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ids := ulid.NewGenerator()
	ords := risktest.NewOrders(ids, time.Now().UTC())
	m, err := risk.New(risk.Deps{Pool: pool, Orders: ords, PhoneKey: []byte(strings.Repeat("k", 32)), Jobs: testkit.NewJobs()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, store := risktest.Store(t, pool, ids)
	o := ords.Order(ctx, risktest.Phone(t, ids), 1, orders.StatusPending, orders.ReasonNone)
	deliver := func(ev event.Event) error {
		t.Helper()
		b, mErr := json.Marshal(ev)
		if mErr != nil {
			t.Fatal(mErr)
		}
		return m.LearnHandler()(ctx, eventbus.Delivery{Meta: ev.EventMeta(), Payload: b})
	}
	meta := event.Meta{ID: ids.New(), Type: orders.EventOrderPlaced, Version: 1, StoreID: store, OccurredAt: time.Now()}
	if err = deliver(orders.OrderPlaced{Meta: meta, OrderID: o.ID}); err != nil {
		t.Fatal(err)
	}
	o.Status = orders.StatusDelivered
	ords.Put(ctx, o)
	meta.Type = orders.EventOrderStatusChanged
	if err = deliver(orders.OrderStatusChanged{Meta: meta, OrderID: o.ID, From: orders.StatusShipped, To: orders.StatusDelivered}); err != nil {
		t.Fatal(err)
	}
	if err = deliver(orders.OrderStatusChanged{Meta: meta, OrderID: ids.New()}); err != nil {
		t.Fatalf("an order gone: %v", err)
	}

	// Through the module's routes: the order is learnt, delivered.
	pol, err := authz.NewPolicy(map[authz.Role][]authz.Permission{authz.RoleOwner: authz.Permissions()})
	if err != nil {
		t.Fatal(err)
	}
	resp := problem.NewResponder(slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	m.Mount(mux, pol, testkit.FakeStoreScope(resp, map[string]testkit.FakeMember{
		"owner": {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{store: authz.RoleOwner}},
	}), resp)
	rq := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/stores/"+store.String()+"/risk/bands",
		strings.NewReader(`{"order_ids":["`+o.ID.String()+`"]}`))
	rq.Header.Set("Authorization", "Bearer owner")
	rq.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, rq)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"band":"low"`) {
		t.Fatalf("bands = %d %s", rec.Code, rec.Body.String())
	}
	if len(m.PeriodicJobs()) != 1 {
		t.Fatal("the purge is the module's periodic job")
	}
}
