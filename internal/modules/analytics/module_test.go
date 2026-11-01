package analytics_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/analytics"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

// noOrders finds no order: what an event about an order gone sees.
type noOrders struct{}

func (noOrders) Get(context.Context, ulid.ID) (orders.Order, error) {
	return orders.Order{}, orders.ErrOrderNotFound
}
func (noOrders) Before(context.Context, ulid.ID, int) ([]orders.Order, error) {
	return nil, nil
}
func (noOrders) BeforeWithLines(context.Context, ulid.ID, int) ([]orders.Order, error) {
	return nil, nil
}

func TestModule(t *testing.T) {
	t.Parallel()
	pool, err := pgxpool.New(t.Context(), infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	stores := func(context.Context) (analytics.StoreInfo, error) {
		return analytics.StoreInfo{Currency: "EGP", Location: time.UTC}, nil
	}
	if _, err = analytics.New(analytics.Deps{Pool: pool, Stores: stores}); err == nil {
		t.Fatal("New without IDs must fail")
	}
	ids := ulid.NewGenerator()
	m, err := analytics.New(analytics.Deps{Pool: pool, Stores: stores, IDs: ids, Orders: noOrders{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.PeriodicJobs()) != 2 {
		t.Fatal("the rollups and the sweeps")
	}
	store := tenant.StoreID{ID: ids.New()}
	ctx := tenant.WithStore(t.Context(), store)
	ev := orders.OrderStatusChanged{Meta: event.Meta{ID: ids.New(), Type: orders.EventOrderStatusChanged, Version: 1, StoreID: store,
		OccurredAt: time.Now()}, OrderID: ids.New()}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.LearnHandler()(ctx, eventbus.Delivery{Meta: ev.Meta, Payload: b}); err != nil {
		t.Fatalf("an order gone teaches nothing: %v", err)
	}
}
