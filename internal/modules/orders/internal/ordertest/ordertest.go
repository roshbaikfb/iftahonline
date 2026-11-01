// Package ordertest builds what the orders module's tests need around
// it: real catalog and customers modules on a migrated database, stores,
// products and blocked phones created through those modules' own APIs,
// recording audit and event fakes, and a movable ID clock. Only tests
// import it.
package ordertest

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
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
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Recorder keeps audit entries as "action target"; it fails on any entry
// that carries a phone number.
type Recorder struct {
	mu      sync.Mutex
	entries []string
}

// RecordTx implements audit.Recorder.
func (r *Recorder) RecordTx(_ context.Context, _ pgx.Tx, e audit.Entry) error {
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

// Take returns and forgets the entries so far.
func (r *Recorder) Take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.entries
	r.entries = nil
	return out
}

// EventLog keeps published events as "type order_id [from->to]".
type EventLog struct {
	mu  sync.Mutex
	got []string
}

// PublishTx implements eventbus.Publisher.
func (l *EventLog) PublishTx(_ context.Context, tx pgx.Tx, ev event.Event) error {
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

// Take returns and forgets the events so far.
func (l *EventLog) Take() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.got
	l.got = nil
	return out
}

var _ eventbus.Publisher = (*EventLog)(nil)

// Policy is the P4 part of identity's role matrix (identity's TestMatrix
// pins the real one).
func Policy(t testing.TB) authz.Policy {
	t.Helper()
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

// Env is a migrated database with the catalog and customers modules, and
// an orders service built on them.
type Env struct {
	Pool      *pgxpool.Pool
	QC        *testkit.QueryCounter
	IDs       *ulid.Generator
	Catalog   catalog.Catalog
	Customers customers.Customers
	Audit     *Recorder
	Events    *EventLog
	Policy    authz.Policy
	// Shipping quotes delivery for services built after it is set; nil
	// charges nothing.
	Shipping app.Shipping
	// Coupons applies codes for services built after it is set; nil
	// makes every code not_found.
	Coupons app.Coupons
	// Plan counts orders for services built after it is set; nil counts
	// nothing.
	Plan entitlements.Entitlements
	// Risk applies risk rules for services built after it is set; nil
	// applies none.
	Risk app.RiskCheck
	// Cards says whether deliveries can be prepaid, for services built
	// after it is set; nil cannot.
	Cards app.CardCheck
	Svc   *app.Service
	// Members are the bearer tokens of FakeStoreScope; NewStore adds an
	// owner per store ("owner-<store id>").
	Members map[string]testkit.FakeMember
	api     http.Handler
	clock   *atomic.Int64
}

// Stores is the StoreSource every test store uses: Egypt, EGP, Cairo.
func Stores(context.Context) (app.StoreInfo, error) {
	cairo, err := time.LoadLocation("Africa/Cairo")
	if err != nil {
		return app.StoreInfo{}, err
	}
	return app.StoreInfo{Currency: money.EGP, Country: geo.EG, Location: cairo}, nil
}

// New builds an Env on the database at appURL (app_user).
func New(t testing.TB, appURL string) *Env {
	t.Helper()
	pool, qc := testkit.CountingPool(t, appURL+"&pool_max_conns=32")
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
	pol := Policy(t)
	e := &Env{Pool: pool, QC: qc, IDs: ids, Catalog: cat.Catalog(), Customers: cust.Customers(), Audit: &Recorder{}, Events: &EventLog{},
		Policy: pol, Members: map[string]testkit.FakeMember{}, clock: offset}
	e.Svc = e.Service(pool)
	resp := problem.NewResponder(log)
	mux := http.NewServeMux()
	cat.Mount(mux, pol, testkit.FakeStoreScope(resp, e.Members), resp)
	cust.Mount(mux, pol, testkit.FakeStoreScope(resp, e.Members), resp)
	e.api = mux
	return e
}

// Service builds an orders service on db (e.g. a capturing pool) with the
// Env's modules and fakes.
func (e *Env) Service(db postgres.Beginner) *app.Service {
	return app.NewService(app.Deps{DB: db, IDs: e.IDs, Audit: e.Audit, Events: e.Events, Catalog: e.Catalog,
		Customers: e.Customers, Shipping: e.Shipping, Coupons: e.Coupons, Auth: e.Policy, Stores: Stores, Plan: e.Plan, Risk: e.Risk, Cards: e.Cards})
}

// NewStore creates a store (EGP, Cairo) and returns its context, with no
// principal (as the storefront calls), and its ID.
func (e *Env) NewStore(t testing.TB) (context.Context, tenant.StoreID) {
	t.Helper()
	id := tenant.StoreID{ID: e.IDs.New()}
	ctx := tenant.WithStore(t.Context(), id)
	if err := postgres.InTenantTx(ctx, e.Pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
			id.ID, "ord-"+strings.ToLower(id.String()[14:]))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	e.Members["owner-"+id.String()] = testkit.FakeMember{UserID: e.IDs.New(), Roles: map[tenant.StoreID]authz.Role{id: authz.RoleOwner}}
	return ctx, id
}

// As returns ctx with a member of role in ctx's store signed in. ctx
// must carry a store (NewStore's context does).
func As(ctx context.Context, t testing.TB, role authz.Role) context.Context {
	t.Helper()
	store, err := tenant.FromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return authz.WithPrincipal(ctx, authz.Principal{UserID: ulid.NewGenerator().New(), StoreID: store, Role: role})
}

// Product creates an active product with one variant through the
// catalog's API and returns the variant's ID.
func (e *Env) Product(t testing.TB, store tenant.StoreID, title string, priceMinor, onHand int) ulid.ID {
	t.Helper()
	return e.productBody(t, store, fmt.Sprintf(`{"title":%q,"status":"active","variants":[{"price_minor":%d,"on_hand":%d}]}`, title, priceMinor, onHand))
}

// ProductWithCost is Product with the merchant's cost (ADR-0068).
func (e *Env) ProductWithCost(t testing.TB, store tenant.StoreID, title string, priceMinor, costMinor, onHand int) ulid.ID {
	t.Helper()
	return e.productBody(t, store, fmt.Sprintf(`{"title":%q,"status":"active","variants":[{"price_minor":%d,"cost_minor":%d,"on_hand":%d}]}`,
		title, priceMinor, costMinor, onHand))
}

func (e *Env) productBody(t testing.TB, store tenant.StoreID, body string) ulid.ID {
	t.Helper()
	var p struct {
		Variants []struct {
			ID string `json:"id"`
		} `json:"variants"`
	}
	if err := json.Unmarshal(e.call(t, store, http.MethodPost, "/products", body, http.StatusCreated), &p); err != nil || len(p.Variants) != 1 {
		t.Fatalf("product: %v", err)
	}
	id, err := ulid.Parse(p.Variants[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// BlockPhone blocks a number in store through the customers API.
func (e *Env) BlockPhone(t testing.TB, store tenant.StoreID, number string) {
	t.Helper()
	e.call(t, store, http.MethodPost, "/customers/blocks", fmt.Sprintf(`{"phone":%q}`, number), http.StatusOK)
}

func (e *Env) call(t testing.TB, store tenant.StoreID, method, path, body string, want int) []byte {
	t.Helper()
	rq := httptest.NewRequestWithContext(t.Context(), method, "/v1/stores/"+store.String()+path, strings.NewReader(body))
	rq.Header.Set("Authorization", "Bearer owner-"+store.String())
	rq.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.api.ServeHTTP(rec, rq)
	if rec.Code != want {
		t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body)
	}
	return rec.Body.Bytes()
}

// Stock returns a variant's (on_hand, reserved).
func (e *Env) Stock(ctx context.Context, t testing.TB, v ulid.ID) (int, int) {
	t.Helper()
	sv, err := e.Catalog.GetSellableVariant(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	return sv.Stock.OnHand, sv.Stock.Reserved
}

// Advance moves the ID clock (and so new orders' created_at) forward.
func (e *Env) Advance(d time.Duration) { e.clock.Add(int64(d)) }
