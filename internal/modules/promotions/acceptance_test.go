package promotions_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/customers"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/promotions"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
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

// coupons adapts promotions to the orders port, as cmd/platform does.
type coupons struct{ c promotions.Coupons }

func (o coupons) Preview(ctx context.Context, code string, subtotal money.Money) (orders.CouponDiscount, error) {
	return toOrder(o.c.Preview(ctx, code, subtotal))
}

func (o coupons) RedeemTx(ctx context.Context, tx pgx.Tx, code string, subtotal money.Money, customer, order ulid.ID) (orders.CouponDiscount, error) {
	return toOrder(o.c.RedeemTx(ctx, tx, code, subtotal, customer, order))
}

func toOrder(d promotions.Discount, err error) (orders.CouponDiscount, error) {
	var r *promotions.Rejected
	if errors.As(err, &r) {
		return orders.CouponDiscount{}, &orders.CouponRejected{Reason: string(r.Reason), MinSubtotalMinor: r.MinSubtotalMinor}
	}
	return orders.CouponDiscount{Amount: d.Amount, FreeShipping: d.FreeShipping}, err
}

// platform is catalog, customers, shipping, orders and promotions wired
// as cmd/platform wires them, with River running the event bus.
type platform struct {
	api     http.Handler
	shop    orders.Shop
	store   tenant.StoreID
	ctx     context.Context
	variant ulid.ID
}

func newPlatform(t *testing.T) platform {
	t.Helper()
	ctx := t.Context()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pool, err := pgxpool.New(ctx, infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	opts, err := redis.ParseURL(infra.RedisURL)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })
	ids := ulid.NewGenerator()
	ins, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Middleware: jobs.Middleware()})
	if err != nil {
		t.Fatal(err)
	}
	bus := eventbus.New(ins)
	keyring, err := secretbox.ParseKeyring("1:ZGV2LW9ubHktc2VjcmV0Ym94LWtleS0zMi1ieXRlcyE=") // public dev key
	if err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.New(catalog.Deps{Pool: pool, Jobs: ins, IDs: ids, Log: log, Keyring: keyring, Events: eventbus.Nop{},
		Media: config.Media{S3Endpoint: "http://127.0.0.1:1", S3PublicEndpoint: "http://127.0.0.1:1", S3Region: "us-east-1", S3Bucket: "b",
			ImgproxyURL: "https://img.test", ImgproxyKey: []byte("0123456789abcdef"), ImgproxySalt: []byte("salt-0123456789a"),
			MaxUploadBytes: 1 << 20, MaxImagePixels: 1 << 20},
		Search:   config.Search{URL: "http://127.0.0.1:1", Index: "products"},
		Currency: catalog.CurrencyFunc(func(context.Context) (money.Currency, error) { return money.EGP, nil })})
	if err != nil {
		t.Fatal(err)
	}
	cust, err := customers.New(customers.Deps{Pool: pool, IDs: ids, Region: func(context.Context) (phone.Region, error) { return phone.EG, nil }})
	if err != nil {
		t.Fatal(err)
	}
	ship, err := shipping.New(shipping.Deps{Pool: pool, IDs: ids, Redis: rdb, Log: log, Events: bus,
		Stores: func(context.Context) (shipping.StoreInfo, error) {
			return shipping.StoreInfo{Currency: money.EGP, Country: geo.EG}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	prom, err := promotions.New(promotions.Deps{Pool: pool, IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	pol, err := authz.NewPolicy(map[authz.Role][]authz.Permission{authz.RoleOwner: authz.Permissions()})
	if err != nil {
		t.Fatal(err)
	}
	ord, err := orders.New(orders.Deps{Pool: pool, IDs: ids, Events: bus, Catalog: cat.Catalog(), Customers: cust.Customers(),
		Shipping: ship.Quoter(), Coupons: coupons{prom.Coupons()}, Auth: pol,
		Stores: func(context.Context) (orders.StoreInfo, error) {
			return orders.StoreInfo{Currency: money.EGP, Country: geo.EG, Location: time.UTC}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	if err = bus.Subscribe(orders.EventOrderStatusChanged, promotions.ReleaseCouponsSubscriber, prom.OrderStatusHandler()); err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	if err = river.AddWorkerSafely(workers, bus.Worker()); err != nil {
		t.Fatal(err)
	}
	rc, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Logger: log, Workers: workers, Middleware: jobs.Middleware(), TestOnly: true,
		Queues: map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 4}}})
	if err != nil {
		t.Fatal(err)
	}
	runCtx := context.WithoutCancel(ctx)
	if err = rc.Start(runCtx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop, cancel := context.WithTimeout(runCtx, 10*time.Second)
		defer cancel()
		_ = rc.Stop(stop)
	})
	store := tenant.StoreID{ID: ids.New()}
	sctx := tenant.WithStore(ctx, store)
	if err = postgres.InTenantTx(sctx, pool, func(tx pgx.Tx) error {
		_, execErr := tx.Exec(sctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
			store.ID, "pac-"+strings.ToLower(store.String()[14:]))
		return execErr
	}); err != nil {
		t.Fatal(err)
	}
	resp := problem.NewResponder(log)
	scope := testkit.FakeStoreScope(resp, map[string]testkit.FakeMember{"owner": {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{store: authz.RoleOwner}}})
	api := http.NewServeMux()
	cat.Mount(api, pol, scope, resp)
	ord.Mount(api, pol, scope, resp)
	prom.Mount(api, pol, scope, resp)
	p := platform{api: api, shop: ord.Shop(), store: store, ctx: sctx}
	prod := p.call(t, http.MethodPost, "/products", `{"title":"ساعة","slug":"watch","status":"active","variants":[{"price_minor":20000,"on_hand":50}]}`, http.StatusCreated)
	if p.variant, err = ulid.Parse(prod["variants"].([]any)[0].(map[string]any)["id"].(string)); err != nil {
		t.Fatal(err)
	}
	return p
}

func (p platform) call(t *testing.T, method, path, body string, want int) map[string]any {
	t.Helper()
	rq := httptest.NewRequestWithContext(t.Context(), method, "/v1/stores/"+p.store.String()+path, strings.NewReader(body))
	rq.Header.Set("Authorization", "Bearer owner")
	rq.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.api.ServeHTTP(rec, rq)
	if rec.Code != want {
		t.Fatalf("%s %s = %d, want %d: %s", method, path, rec.Code, want, rec.Body.String())
	}
	var m map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

func (p platform) place(phoneNumber, code string) (orders.Placement, error) {
	return p.placeQty(phoneNumber, code, 1)
}

// placeQty places qty watches (another qty is not a duplicate of an
// order just placed).
func (p platform) placeQty(phoneNumber, code string, qty int) (orders.Placement, error) {
	return p.shop.PlaceOrder(p.ctx, orders.NewOrder{Source: orders.SourceStorefront, Name: "منى أحمد", Phone: phoneNumber,
		Governorate: "EG-C", City: "مدينة نصر", Address: "12 شارع عباس العقاد", CouponCode: code,
		Lines: []orders.LineInput{{VariantID: p.variant, Qty: qty}}})
}

func (p platform) uses(t *testing.T, id string) float64 {
	t.Helper()
	return p.call(t, http.MethodGet, "/coupons/"+id, "", http.StatusOK)["uses"].(float64)
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestCouponOnOrders is P6's coupon criterion end to end: a merchant's
// coupon takes its discount off a placed order (stored and shown); an
// order cancelled before it ships gives its use back; one customer cannot
// use a once-per-customer code twice.
func TestCouponOnOrders(t *testing.T) {
	p := newPlatform(t)
	c := p.call(t, http.MethodPost, "/coupons", `{"code":"eid25","kind":"percent","percent":25,"max_per_customer":1,"active":true}`, http.StatusCreated)
	id := c["id"].(string)

	placed, err := p.place("01012345678", "eid25")
	if err != nil {
		t.Fatal(err)
	}
	if placed.Order.Discount.Amount() != 5000 || placed.Order.CouponCode != "EID25" || placed.Order.Total.Amount() != 15000 {
		t.Fatalf("order = discount %v code %q total %v", placed.Order.Discount, placed.Order.CouponCode, placed.Order.Total)
	}
	o := p.call(t, http.MethodGet, "/orders/"+placed.Order.ID.String(), "", http.StatusOK)["order"].(map[string]any)
	if o["discount_minor"] != float64(5000) || o["coupon_code"] != "EID25" || o["total_minor"] != float64(15000) {
		t.Fatalf("order API = %v", o)
	}
	if p.uses(t, id) != 1 {
		t.Fatal("use not counted")
	}
	// The same customer again (another order): refused, nothing placed.
	_, err = p.placeQty("010 1234 5678", "EID25", 2)
	var e *errs.Error
	if !errors.As(err, &e) || len(e.Fields) != 1 || e.Fields[0].Code != "per_customer" {
		t.Fatalf("second use by the customer: %v", err)
	}
	// Confirmed, then cancelled before shipping: the use comes back, and
	// the customer may use it again.
	p.call(t, http.MethodPost, "/orders/"+placed.Order.ID.String()+"/transitions", `{"status":"confirmed"}`, http.StatusOK)
	p.call(t, http.MethodPost, "/orders/"+placed.Order.ID.String()+"/transitions", `{"status":"cancelled"}`, http.StatusOK)
	eventually(t, "the use to come back", func() bool { return p.uses(t, id) == 0 })
	if _, err = p.placeQty("01012345678", "EID25", 3); err != nil {
		t.Fatalf("use after the release: %v", err)
	}
	// The coupon an order held keeps its code and cannot be deleted.
	p.call(t, http.MethodDelete, "/coupons/"+id, "", http.StatusConflict)
}

// TestLastUseByConcurrentOrders: five shoppers race for a coupon's one
// use; one order takes it, the others are refused and place nothing.
func TestLastUseByConcurrentOrders(t *testing.T) {
	p := newPlatform(t)
	c := p.call(t, http.MethodPost, "/coupons", `{"code":"ONLY1","kind":"free_shipping","max_uses":1,"active":true}`, http.StatusCreated)
	var wg sync.WaitGroup
	results := make(chan error, 5)
	for i := range 5 {
		wg.Go(func() {
			_, err := p.place(fmt.Sprintf("0101234560%d", i), "ONLY1")
			results <- err
		})
	}
	wg.Wait()
	close(results)
	won, usedUp := 0, 0
	for err := range results {
		var e *errs.Error
		switch {
		case err == nil:
			won++
		case errors.As(err, &e) && len(e.Fields) == 1 && e.Fields[0].Code == "used_up":
			usedUp++
		default:
			t.Fatalf("unexpected: %v", err)
		}
	}
	if won != 1 || usedUp != 4 || p.uses(t, c["id"].(string)) != 1 {
		t.Fatalf("won %d, used up %d", won, usedUp)
	}
	list := p.call(t, http.MethodGet, "/orders", "", http.StatusOK)["items"].([]any)
	if len(list) != 1 {
		t.Fatalf("%d orders placed, want 1", len(list))
	}
}
