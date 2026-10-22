package app_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/customers"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/ordertest"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

// fixture names the shared environment the way these tests read.
type fixture struct {
	t        *testing.T
	env      *ordertest.Env
	svc      *app.Service
	pool     *pgxpool.Pool
	qc       *testkit.QueryCounter
	cust     customers.Customers
	catalog  catalog.Catalog
	audit    *ordertest.Recorder
	events   *ordertest.EventLog
	ctxA     context.Context // store A's owner
	ctxB     context.Context // store B's owner
	shopperA context.Context // store A, no principal (the storefront)
	storeA   tenant.StoreID
	storeB   tenant.StoreID
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	e := ordertest.New(t, infra.AppURL)
	shopperA, a := e.NewStore(t)
	ctxB, b := e.NewStore(t)
	return fixture{t: t, env: e, svc: e.Svc, pool: e.Pool, qc: e.QC, cust: e.Customers, catalog: e.Catalog, audit: e.Audit, events: e.Events,
		ctxA: ordertest.As(shopperA, t, authz.RoleOwner), ctxB: ordertest.As(ctxB, t, authz.RoleOwner), shopperA: shopperA, storeA: a, storeB: b}
}

func (f fixture) asRole(ctx context.Context, role authz.Role) context.Context {
	return ordertest.As(ctx, f.t, role)
}

func (f fixture) product(t *testing.T, store tenant.StoreID, title string, priceMinor, onHand int) ulid.ID {
	t.Helper()
	return f.env.Product(t, store, title, priceMinor, onHand)
}

func (f fixture) stock(ctx context.Context, t *testing.T, v ulid.ID) (int, int) {
	t.Helper()
	return f.env.Stock(ctx, t, v)
}

func (f fixture) advance(d time.Duration) { f.env.Advance(d) }

func blockPhone(t *testing.T, f fixture, number string) {
	t.Helper()
	f.env.BlockPhone(t, f.storeA, number)
}
