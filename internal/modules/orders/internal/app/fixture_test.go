package app_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/customers"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/secretbox"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

// recorder keeps audit entries; it fails on any that carries a phone.
type recorder struct {
	mu      sync.Mutex
	entries []string
}

func (r *recorder) RecordTx(_ context.Context, _ pgx.Tx, e audit.Entry) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if strings.Contains(fmt.Sprint(e), "+20") {
		return fmt.Errorf("audit entry carries a phone: %+v", e)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e.Action+" "+e.TargetID)
	return nil
}

func (r *recorder) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.entries
	r.entries = nil
	return out
}

// eventLog keeps published events as "type order_id [from->to]".
type eventLog struct {
	mu  sync.Mutex
	got []string
}

func (l *eventLog) PublishTx(_ context.Context, tx pgx.Tx, ev event.Event) error {
	if tx == nil || event.ValidateType(ev.EventMeta().Type) != nil {
		return fmt.Errorf("bad publish %+v", ev.EventMeta())
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	var m map[string]any
	if err = json.Unmarshal(b, &m); err != nil {
		return err
	}
	line := fmt.Sprintf("%s %v", ev.EventMeta().Type, m["order_id"])
	if m["to"] != nil {
		line += fmt.Sprintf(" %v->%v", m["from"], m["to"])
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.got = append(l.got, line)
	return nil
}

func (l *eventLog) take() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.got
	l.got = nil
	return out
}

var _ eventbus.Publisher = (*eventLog)(nil)

type fixture struct {
	svc      *app.Service
	pool     *pgxpool.Pool
	qc       *testkit.QueryCounter
	catalog  catalog.Catalog
	cust     customers.Customers
	custMod  *customers.Module
	audit    *recorder
	events   *eventLog
	api      http.Handler // the catalog's and customers' merchant APIs
	members  map[string]testkit.FakeMember
	clock    *atomic.Int64 // offset added to the ID clock, in ns
	ctxA     context.Context
	ctxB     context.Context
	shopperA context.Context // store A, no principal (the storefront)
	storeA   tenant.StoreID
	storeB   tenant.StoreID
}

// asRole returns store A's context with a member of role signed in.
func (f fixture) asRole(ctx context.Context, role authz.Role) context.Context {
	store, _ := tenant.FromContext(ctx)
	return authz.WithPrincipal(ctx, authz.Principal{UserID: ulid.NewGenerator().New(), StoreID: store, Role: role})
}

func policy(t *testing.T) authz.Policy {
	t.Helper()
	// The P4 rows of identity's matrix (identity's TestMatrix pins them).
	pol, err := authz.NewPolicy(map[authz.Role][]authz.Permission{
		authz.RoleOwner:    authz.Permissions(),
		authz.RoleSupport:  {authz.PermOrdersRead, authz.PermOrdersManage, authz.PermCustomersRead, authz.PermCustomersManage},
		authz.RoleShipping: {authz.PermOrdersRead, authz.PermOrdersFulfil, authz.PermCatalogRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	return pol
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := t.Context()
	pool, qc := testkit.CountingPool(t, infra.AppURL+"&pool_max_conns=32")
	ins, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Middleware: jobs.Middleware()})
	if err != nil {
		t.Fatal(err)
	}
	offset := &atomic.Int64{}
	ids := ulid.NewGeneratorWith(func() time.Time { return time.Now().Add(time.Duration(offset.Load())) }, rand.Reader)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	keyring, err := secretbox.ParseKeyring("1:ZGV2LW9ubHktc2VjcmV0Ym94LWtleS0zMi1ieXRlcyE=") // public dev key
	if err != nil {
		t.Fatal(err)
	}
	media := config.Media{ // nothing here is contacted: no images in these tests
		S3Endpoint: "http://127.0.0.1:1", S3PublicEndpoint: "http://127.0.0.1:1", S3Region: "us-east-1", S3Bucket: "bucket",
		ImgproxyURL: "https://img.test", ImgproxyKey: []byte("0123456789abcdef"), ImgproxySalt: []byte("salt-0123456789a"),
		MaxUploadBytes: 1 << 20, MaxImagePixels: 1 << 20,
	}
	cat, err := catalog.New(catalog.Deps{
		Pool: pool, Jobs: ins, IDs: ids, Log: log, Keyring: keyring, Events: eventbus.Nop{}, Media: media,
		Search:   config.Search{URL: "http://127.0.0.1:1", Index: "products"},
		Currency: catalog.CurrencyFunc(func(context.Context) (money.Currency, error) { return money.EGP, nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	cust, err := customers.New(customers.Deps{Pool: pool, IDs: ids,
		Region: func(context.Context) (phone.Region, error) { return phone.EG, nil }})
	if err != nil {
		t.Fatal(err)
	}
	cairo, err := time.LoadLocation("Africa/Cairo")
	if err != nil {
		t.Fatal(err)
	}
	pol := policy(t)
	rec, evs := &recorder{}, &eventLog{}
	svc := app.NewService(app.Deps{DB: pool, IDs: ids, Audit: rec, Events: evs, Catalog: cat.Catalog(), Customers: cust.Customers(),
		Auth: pol, Stores: func(context.Context) (app.StoreInfo, error) {
			return app.StoreInfo{Currency: money.EGP, Country: geo.EG, Location: cairo}, nil
		}})

	resp := problem.NewResponder(log)
	members := map[string]testkit.FakeMember{}
	mux := http.NewServeMux()
	cat.Mount(mux, pol, testkit.FakeStoreScope(resp, members), resp)
	cust.Mount(mux, pol, testkit.FakeStoreScope(resp, members), resp)

	mk := func() (context.Context, tenant.StoreID) {
		id := tenant.StoreID{ID: ids.New()}
		sctx := tenant.WithStore(ctx, id)
		if txErr := postgres.InTenantTx(sctx, pool, func(tx pgx.Tx) error {
			_, execErr := tx.Exec(sctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
				id.ID, "ord-"+strings.ToLower(id.String()[14:]))
			return execErr
		}); txErr != nil {
			t.Fatal(txErr)
		}
		members["owner-"+id.String()] = testkit.FakeMember{UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{id: authz.RoleOwner}}
		return sctx, id
	}
	ctxA, a := mk()
	ctxB, b := mk()
	f := fixture{svc: svc, pool: pool, qc: qc, catalog: cat.Catalog(), cust: cust.Customers(), custMod: cust, audit: rec, events: evs,
		api: mux, members: members, clock: offset, shopperA: ctxA, storeA: a, storeB: b}
	f.ctxA = f.asRole(ctxA, authz.RoleOwner)
	f.ctxB = f.asRole(ctxB, authz.RoleOwner)
	return f
}

// product creates an active product with one variant through the
// catalog's API and returns the variant's ID.
func (f fixture) product(t *testing.T, store tenant.StoreID, title string, priceMinor, onHand int) ulid.ID {
	t.Helper()
	body := fmt.Sprintf(`{"title":%q,"status":"active","variants":[{"price_minor":%d,"on_hand":%d,"option_values":[]}]}`, title, priceMinor, onHand)
	rq := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/stores/"+store.String()+"/products", strings.NewReader(body))
	rq.Header.Set("Authorization", "Bearer owner-"+store.String())
	rq.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.api.ServeHTTP(rec, rq)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create product: %d %s", rec.Code, rec.Body)
	}
	var p struct {
		Variants []struct {
			ID string `json:"id"`
		} `json:"variants"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || len(p.Variants) != 1 {
		t.Fatalf("product %s: %v", rec.Body, err)
	}
	id, err := ulid.Parse(p.Variants[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// blockPhone blocks a number in store A through the customers API.
func blockPhone(t *testing.T, f fixture, number string) {
	t.Helper()
	rq := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/stores/"+f.storeA.String()+"/customers/blocks",
		strings.NewReader(fmt.Sprintf(`{"phone":%q}`, number)))
	rq.Header.Set("Authorization", "Bearer owner-"+f.storeA.String())
	rq.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.api.ServeHTTP(rec, rq)
	if rec.Code != http.StatusOK {
		t.Fatalf("block phone: %d %s", rec.Code, rec.Body)
	}
}

// stock returns a variant's (on_hand, reserved).
func (f fixture) stock(ctx context.Context, t *testing.T, v ulid.ID) (int, int) {
	t.Helper()
	sv, err := f.catalog.GetSellableVariant(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	return sv.Stock.OnHand, sv.Stock.Reserved
}

// advance moves the order-ID clock (and so created_at) forward.
func (f fixture) advance(d time.Duration) { f.clock.Add(int64(d)) }
