package payments_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
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
	"github.com/roshbaikfb/iftahonline/internal/modules/payments"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/envelope"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/ratelimit"
	"github.com/roshbaikfb/iftahonline/internal/platform/secretbox"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

const (
	apiKey     = "merchant-api-key"
	hookSecret = "merchant-hook-secret"
)

// platform is the api and worker roles in one process, wired as
// cmd/platform wires them: catalog, customers, shipping (quotes),
// orders, payments and the webhooks inbox, with River running the jobs.
type platform struct {
	api, public http.Handler
	shop        orders.Shop
	checkout    payments.Checkout
	payer       payments.FakePayer
	store       tenant.StoreID
	ctx         context.Context
	token       string
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
	// Inserts go through a client without workers (as the api role's),
	// so jobs of kinds this test does not run (search indexing) can wait.
	ins, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Middleware: jobs.Middleware()})
	if err != nil {
		t.Fatal(err)
	}
	bus := eventbus.New(ins)
	keyring, err := secretbox.ParseKeyring("1:ZGV2LW9ubHktc2VjcmV0Ym94LWtleS0zMi1ieXRlcyE=") // public dev key
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := envelope.New(envelope.FromKeyring(keyring))
	if err != nil {
		t.Fatal(err)
	}
	mediaCfg := config.Media{S3Endpoint: "http://127.0.0.1:1", S3PublicEndpoint: "http://127.0.0.1:1", S3Region: "us-east-1", S3Bucket: "b",
		ImgproxyURL: "https://img.test", ImgproxyKey: []byte("0123456789abcdef"), ImgproxySalt: []byte("salt-0123456789a"),
		MaxUploadBytes: 1 << 20, MaxImagePixels: 1 << 20}
	cat, err := catalog.New(catalog.Deps{Pool: pool, Jobs: ins, IDs: ids, Log: log, Keyring: keyring, Events: eventbus.Nop{}, Media: mediaCfg,
		Search:   config.Search{URL: "http://127.0.0.1:1", Index: "products"},
		Currency: catalog.CurrencyFunc(func(context.Context) (money.Currency, error) { return money.EGP, nil })})
	if err != nil {
		t.Fatal(err)
	}
	cust, err := customers.New(customers.Deps{Pool: pool, IDs: ids, Region: func(context.Context) (phone.Region, error) { return phone.EG, nil }})
	if err != nil {
		t.Fatal(err)
	}
	inboxLimiter, err := ratelimit.New(rdb, "acc-"+strings.ToLower(ids.New().String()))
	if err != nil {
		t.Fatal(err)
	}
	hooks, err := webhooks.New(webhooks.Deps{Pool: pool, IDs: ids, Limiter: inboxLimiter, Log: log,
		ClientIP: func(context.Context) (netip.Addr, bool) { return netip.MustParseAddr("198.51.100.21"), true }})
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
	pol, err := authz.NewPolicy(map[authz.Role][]authz.Permission{authz.RoleOwner: authz.Permissions()})
	if err != nil {
		t.Fatal(err)
	}
	ord, err := orders.New(orders.Deps{Pool: pool, IDs: ids, Events: bus, Catalog: cat.Catalog(), Customers: cust.Customers(),
		Shipping: ship.Quoter(), Auth: pol,
		Stores: func(context.Context) (orders.StoreInfo, error) {
			return orders.StoreInfo{Currency: money.EGP, Country: geo.EG, Location: time.UTC}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	pay, err := payments.New(payments.Deps{Pool: pool, IDs: ids, Events: bus, Gateways: config.Payments{FakeGateway: true},
		Sealer: sealer, PublicURL: "https://app.example.com", Orders: ord.Payments(), Jobs: ins, Inbox: hooks.Inbox()})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pay.WebhookProviders() {
		if err = hooks.Register(p); err != nil {
			t.Fatal(err)
		}
	}

	workers := river.NewWorkers()
	if err = river.AddWorkerSafely(workers, bus.Worker()); err != nil {
		t.Fatal(err)
	}
	if err = pay.RegisterWorkers(workers); err != nil {
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
	members := map[string]testkit.FakeMember{"owner": {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{store: authz.RoleOwner}}}
	scope := testkit.FakeStoreScope(resp, members)
	api := http.NewServeMux()
	cat.Mount(api, pol, scope, resp)
	ord.Mount(api, pol, scope, resp)
	pay.Mount(api, pol, scope, resp)
	public := http.NewServeMux()
	hooks.Mount(public, resp)
	return platform{api: api, public: public, shop: ord.Shop(), checkout: pay.Checkout(), payer: pay.FakePayer(), store: store, ctx: sctx,
		token: "owner"}
}

func (p platform) call(t *testing.T, method, path, body string, want int) map[string]any {
	t.Helper()
	rq := httptest.NewRequestWithContext(t.Context(), method, "/v1/stores/"+p.store.String()+path, strings.NewReader(body))
	rq.Header.Set("Authorization", "Bearer "+p.token)
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

// TestWebhookReplayedFiveTimesChangesStateOnce is P5's acceptance for
// payments: the gateway's webhook delivered five times (concurrently) is
// accepted once; the job re-queries the gateway; the payment is marked
// paid and the order confirmed exactly once; a forged delivery is
// refused and changes nothing; a refund goes through the gateway.
func TestWebhookReplayedFiveTimesChangesStateOnce(t *testing.T) {
	p := newPlatform(t)
	prod := p.call(t, http.MethodPost, "/products", `{"title":"ساعة","slug":"watch","status":"active","variants":[{"price_minor":45000,"on_hand":5}]}`, http.StatusCreated)
	variant, err := ulid.Parse(prod["variants"].([]any)[0].(map[string]any)["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	conn := p.call(t, http.MethodPost, "/payments/gateways", `{"gateway":"fake","api_key":"`+apiKey+`","webhook_secret":"`+hookSecret+`"}`, http.StatusCreated)
	webhookPath := strings.TrimPrefix(conn["webhook_url"].(string), "https://app.example.com")

	placed, err := p.shop.PlaceOrder(p.ctx, orders.NewOrder{Source: orders.SourceStorefront, PaymentMethod: orders.PaymentCard,
		Name: "منى أحمد", Phone: "01012345678", Governorate: "EG-C", City: "مدينة نصر", Address: "12 شارع عباس العقاد",
		Lines: []orders.LineInput{{VariantID: variant, Qty: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	orderID := placed.Order.ID.String()
	page, err := p.checkout.StartCheckout(p.ctx, placed.Order.ID, "https://shop.example.com/thank-you")
	if err != nil {
		t.Fatal(err)
	}

	// The shopper pays; the gateway sends its webhook five times at once.
	if err = p.payer.Pay(apiKey, page.PaymentID); err != nil {
		t.Fatal(err)
	}
	body, header, err := p.payer.Webhook(apiKey, hookSecret, page.PaymentID)
	if err != nil {
		t.Fatal(err)
	}
	post := func(h http.Header) (int, string) {
		rq := httptest.NewRequestWithContext(t.Context(), http.MethodPost, webhookPath, strings.NewReader(string(body)))
		rq.Header.Set("Content-Type", "application/json")
		for k, vs := range h {
			rq.Header[k] = vs
		}
		rec := httptest.NewRecorder()
		p.public.ServeHTTP(rec, rq)
		var out struct{ Status string }
		_ = json.Unmarshal(rec.Body.Bytes(), &out) //nolint:errcheck // asserted through the status
		return rec.Code, out.Status
	}
	statuses := make(chan string, 5)
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			code, status := post(header)
			if code != http.StatusOK {
				t.Errorf("delivery: %d", code)
			}
			statuses <- status
		})
	}
	wg.Wait()
	close(statuses)
	accepted := 0
	for s := range statuses {
		if s == "accepted" {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("%d of 5 deliveries accepted, want 1", accepted)
	}
	forged := http.Header{}
	for k := range header {
		forged.Set(k, strings.Repeat("0", 64))
	}
	if code, _ := post(forged); code != http.StatusUnauthorized {
		t.Fatalf("forged: %d", code)
	}
	if code, _ := post(nil); code != http.StatusUnauthorized {
		t.Fatalf("unsigned: %d", code)
	}

	var order map[string]any
	eventually(t, "the order to be paid and confirmed", func() bool {
		order = p.call(t, http.MethodGet, "/orders/"+orderID, "", http.StatusOK)
		o := order["order"].(map[string]any)
		return o["status"] == "confirmed" && o["payment_status"] == "paid"
	})
	time.Sleep(300 * time.Millisecond) // let any duplicate job (there must be none) run
	order = p.call(t, http.MethodGet, "/orders/"+orderID, "", http.StatusOK)
	var byGateway []string
	for _, e := range order["events"].([]any) {
		ev := e.(map[string]any)
		if ev["actor"] == "gateway:fake" {
			byGateway = append(byGateway, ev["to"].(string))
		}
	}
	if strings.Join(byGateway, ",") != "confirmed" {
		t.Fatalf("the gateway's moves of the order: %v, want confirmed once", byGateway)
	}

	pid := page.PaymentID.String()
	p.call(t, http.MethodPost, "/payments/"+pid+"/refunds", `{"amount_minor":15000,"reason":"مرتجع"}`, http.StatusAccepted)
	eventually(t, "the refund to go through", func() bool {
		d := p.call(t, http.MethodGet, "/payments/"+pid, "", http.StatusOK)
		pay := d["payment"].(map[string]any)
		return pay["status"] == "partially_refunded" && pay["refunded_minor"] == float64(15000) &&
			d["refunds"].([]any)[0].(map[string]any)["status"] == "succeeded"
	})
	eventually(t, "the order to follow", func() bool {
		return p.call(t, http.MethodGet, "/orders/"+orderID, "", http.StatusOK)["order"].(map[string]any)["payment_status"] == "partially_refunded"
	})
}
