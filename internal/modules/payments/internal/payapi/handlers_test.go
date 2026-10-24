package payapi_test

import (
	"context"
	"encoding/base64"
	"errors"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway/fake"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/payapi"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/paytest"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/webhookstest"
	"github.com/roshbaikfb/iftahonline/internal/platform/envelope"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
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
	apiKey  = "merchant-api-key"
	hostA   = "souq-a.iftahonline.test"
	hostB   = "souq-b.iftahonline.test"
	apiBase = "https://app.example.com"
)

// countingGateway counts the status inquiries a fake answers.
type countingGateway struct {
	*fake.Gateway
	asked *atomic.Int64
}

func (g countingGateway) FetchStatus(ctx context.Context, c gateway.Credentials, ref string, cur money.Currency) (gateway.Status, error) {
	g.asked.Add(1)
	return g.Gateway.FetchStatus(ctx, c, ref, cur)
}

type fixture struct {
	h              http.Handler
	svc            *app.Service
	gw             *fake.Gateway
	asked          *atomic.Int64
	orders         *paytest.Orders
	storeA, storeB tenant.StoreID
	ctxA           context.Context
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := t.Context()
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
	limiter, err := ratelimit.New(rdb, "pay-"+strings.ToLower(ids.New().String()))
	if err != nil {
		t.Fatal(err)
	}
	k, err := secretbox.ParseKeyring("1:ZGV2LW9ubHktc2VjcmV0Ym94LWtleS0zMi1ieXRlcyE=") // public dev key
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := envelope.New(envelope.FromKeyring(k))
	if err != nil {
		t.Fatal(err)
	}
	f := fixture{gw: fake.NewWithCheckout(apiBase + payapi.FakeCheckoutPath), orders: paytest.NewOrders(), asked: &atomic.Int64{}}
	f.svc = app.NewService(app.Deps{DB: pool, IDs: ids, Gateways: app.Gateways{fake.Name: countingGateway{Gateway: f.gw, asked: f.asked}}, Sealer: sealer, PublicURL: apiBase,
		Orders: f.orders, Jobs: testkit.NewJobs(), Inbox: webhookstest.NewInbox()})
	mk := func() tenant.StoreID {
		id := tenant.StoreID{ID: ids.New()}
		sctx := tenant.WithStore(ctx, id)
		if txErr := postgres.InTenantTx(sctx, pool, func(tx pgx.Tx) error {
			_, execErr := tx.Exec(sctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
				id.ID, "pya-"+strings.ToLower(id.String()[14:]))
			return execErr
		}); txErr != nil {
			t.Fatal(txErr)
		}
		return id
	}
	f.storeA, f.storeB = mk(), mk()
	f.ctxA = tenant.WithStore(ctx, f.storeA)
	if _, err = f.svc.Connect(f.ctxA, app.ConnectInput{Gateway: fake.Name, APIKey: apiKey, WebhookSecret: "s"}); err != nil {
		t.Fatal(err)
	}
	hosts := map[string]tenant.StoreID{hostA: f.storeA, hostA + ":8443": f.storeA, hostB: f.storeB}
	mux := http.NewServeMux()
	payapi.Mount(mux, payapi.Deps{Service: f.svc, Limiter: limiter,
		Hosts: func(_ context.Context, host string) (tenant.StoreID, error) {
			if s, ok := hosts[host]; ok {
				return s, nil
			}
			return tenant.StoreID{}, errors.New("unknown host")
		},
		ClientIP: func(context.Context) (netip.Addr, bool) { return netip.MustParseAddr("198.51.100.30"), true },
		Problems: problem.NewResponder(slog.New(slog.NewTextHandler(io.Discard, nil))), Log: slog.New(slog.DiscardHandler)})
	payapi.MountFakeGateway(mux, f.gw)
	f.h = mux
	return f
}

func (f fixture) get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
	return rec
}

func startPath(store tenant.StoreID, order ulid.ID, back string) string {
	return "/v1/pay/" + store.String() + "/" + order.String() + "?return=" + url.QueryEscape(back)
}

func thanks(order ulid.ID) string { return "https://" + hostA + "/thank-you/" + order.String() }

// TestCheckoutRoundTrip: the shopper goes to the gateway, pays on its
// page, comes back through the return route (which asks the gateway at
// once), and lands on the store's thank-you page with the order paid.
func TestCheckoutRoundTrip(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	order := f.orders.Add(t, orders.PaymentCard, 0)
	rec := f.get(t, startPath(f.storeA, order, thanks(order)))
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), apiBase+payapi.FakeCheckoutPath) ||
		rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("start = %d %v", rec.Code, rec.Header())
	}
	page := strings.TrimPrefix(rec.Header().Get("Location"), apiBase)
	if rec = f.get(t, page); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "520.00 EGP") || !strings.Contains(rec.Body.String(), `value="pay"`) {
		t.Fatalf("fake page = %d %s", rec.Code, rec.Body.String())
	}
	rq := httptest.NewRequestWithContext(t.Context(), http.MethodPost, page, strings.NewReader("answer=pay"))
	rq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	f.h.ServeHTTP(rec, rq)
	back := f.svc.ReturnURL(f.storeA, order, thanks(order))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `content="0;url=`+template.HTMLEscapeString(back)+`"`) ||
		!strings.Contains(rec.Header().Get("Content-Security-Policy"), "form-action 'self'") {
		t.Fatalf("pay = %d %s", rec.Code, rec.Body.String())
	}
	if f.orders.Order(order).PaymentStatus == orders.PaymentPaid {
		t.Fatal("paid before anyone asked the gateway")
	}
	// The gateway adds its own query parameters to the return URL.
	if rec = f.get(t, strings.TrimPrefix(back, apiBase)+"?success=true&id=123"); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != thanks(order) {
		t.Fatalf("return = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	o := f.orders.Order(order)
	if o.PaymentStatus != orders.PaymentPaid || o.Status != orders.StatusConfirmed {
		t.Fatalf("after return: %+v", o)
	}
	// Paid: starting again goes straight back.
	if rec = f.get(t, startPath(f.storeA, order, thanks(order))); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != thanks(order) {
		t.Fatalf("start a paid order = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// The page answers once.
	rq = httptest.NewRequestWithContext(t.Context(), http.MethodPost, page, strings.NewReader("answer=pay"))
	rq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	f.h.ServeHTTP(rec, rq)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second answer = %d", rec.Code)
	}
}

func TestDeclinedThenRetried(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	order := f.orders.Add(t, orders.PaymentCard, 0)
	rec := f.get(t, startPath(f.storeA, order, thanks(order)))
	ref := strings.TrimPrefix(rec.Header().Get("Location"), apiBase+payapi.FakeCheckoutPath)
	if err := f.gw.Answer(ref, false); err != nil {
		t.Fatal(err)
	}
	f.get(t, strings.TrimPrefix(f.svc.ReturnURL(f.storeA, order, thanks(order)), apiBase))
	ps, err := f.svc.OrderPayments(f.ctxA, order)
	if err != nil || len(ps) != 1 || ps[0].Status != domain.StatusFailed || f.orders.Order(order).PaymentStatus != orders.PaymentAwaiting {
		t.Fatalf("declined: %+v %v, order %+v", ps, err, f.orders.Order(order))
	}
	// "Pay now" opens a new page.
	rec = f.get(t, startPath(f.storeA, order, thanks(order)))
	if rec.Code != http.StatusSeeOther || strings.HasSuffix(rec.Header().Get("Location"), ref) {
		t.Fatalf("retry = %d %q, want a new payment", rec.Code, rec.Header().Get("Location"))
	}
}

// TestReturnURLs: the shopper is only ever sent to a host of the same
// store; anything else is 404 before any payment is made.
func TestReturnURLs(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	order := f.orders.Add(t, orders.PaymentCard, 0)
	for name, back := range map[string]string{
		"another store's host": "https://" + hostB + "/thank-you/x",
		"a foreign host":       "https://evil.example/thank-you/x",
		"javascript":           "javascript:alert(1)",
		"no host":              "/thank-you/x",
		"credentials":          "https://user:pw@" + hostA + "/thank-you/x",
		"empty":                "",
		"too long":             "https://" + hostA + "/" + strings.Repeat("x", payapi.MaxReturnURL),
	} {
		if rec := f.get(t, startPath(f.storeA, order, back)); rec.Code != http.StatusNotFound {
			t.Errorf("%s: start = %d %q", name, rec.Code, rec.Header().Get("Location"))
		}
		enc := base64.RawURLEncoding.EncodeToString([]byte(back))
		if rec := f.get(t, "/v1/pay/"+f.storeA.String()+"/"+order.String()+"/return/"+enc); rec.Code != http.StatusNotFound {
			t.Errorf("%s: return = %d %q", name, rec.Code, rec.Header().Get("Location"))
		}
	}
	if ps, err := f.svc.OrderPayments(f.ctxA, order); err != nil || len(ps) != 0 {
		t.Fatalf("payments made for refused returns: %+v %v", ps, err)
	}
	// B's store in the path with A's host: refused too.
	if rec := f.get(t, startPath(f.storeB, order, thanks(order))); rec.Code != http.StatusNotFound {
		t.Errorf("B's path with A's host = %d", rec.Code)
	}
	for _, p := range []string{
		"/v1/pay/nope/" + order.String() + "?return=" + url.QueryEscape(thanks(order)),
		"/v1/pay/" + f.storeA.String() + "/nope?return=" + url.QueryEscape(thanks(order)),
		"/v1/pay/" + f.storeA.String() + "/" + order.String() + "/return/!!!",
		payapi.FakeCheckoutPath + "unknown",
	} {
		if rec := f.get(t, p); rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d", p, rec.Code)
		}
	}
	// A port on the store's host is the same host.
	if rec := f.get(t, startPath(f.storeA, order, "https://"+hostA+":8443/thank-you/"+order.String())); rec.Code != http.StatusSeeOther {
		t.Errorf("with a port = %d", rec.Code)
	}
}

// TestNotPayable: a COD order, or a store that takes no cards, goes
// straight back to the thank-you page.
func TestNotPayable(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	cod := f.orders.Add(t, orders.PaymentCOD, 0)
	if rec := f.get(t, startPath(f.storeA, cod, thanks(cod))); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != thanks(cod) {
		t.Fatalf("COD = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	card := f.orders.Add(t, orders.PaymentCard, 0)
	back := "https://" + hostB + "/thank-you/" + card.String()
	if rec := f.get(t, startPath(f.storeB, card, back)); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != back {
		t.Fatalf("no gateway = %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestRateLimited(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	order := f.orders.Add(t, orders.PaymentCard, 0)
	last := 0
	for range 21 {
		last = f.get(t, startPath(f.storeA, order, thanks(order))).Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("21st checkout = %d, want 429", last)
	}
}

// TestReturnRateLimited: the way back asks the gateway, so it is
// bounded per address too; past the bound the shopper still goes back to
// the store (the webhook brings the payment), without the inquiry.
func TestReturnRateLimited(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	order := f.orders.Add(t, orders.PaymentCard, 0)
	if rec := f.get(t, startPath(f.storeA, order, thanks(order))); rec.Code != http.StatusSeeOther {
		t.Fatalf("start = %d", rec.Code)
	}
	back := strings.TrimPrefix(f.svc.ReturnURL(f.storeA, order, thanks(order)), apiBase)
	for i := range 40 {
		rec := f.get(t, back)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != thanks(order) {
			t.Fatalf("return %d = %d %q, want back to the store", i, rec.Code, rec.Header().Get("Location"))
		}
	}
	if n := f.asked.Load(); n > 30 {
		t.Fatalf("the gateway was asked %d times for 40 returns, want at most 30", n)
	}
}
