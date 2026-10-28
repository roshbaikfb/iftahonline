package messaging_test

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
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel/fake"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/mailer"
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

// mails keeps what was emailed.
type mails struct {
	mu  sync.Mutex
	got []mailer.Message
}

func (m *mails) Send(_ context.Context, msg mailer.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.got = append(m.got, msg)
	return nil
}

func (m *mails) all() []mailer.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]mailer.Message(nil), m.got...)
}

// platform is the api and worker roles in one process, wired as
// cmd/platform wires them: catalog, customers, shipping, orders, the
// webhooks inbox and messaging (with the fake channels and a quota),
// with River running the jobs and the event bus.
type platform struct {
	owner, app *pgxpool.Pool
	api        http.Handler
	public     http.Handler
	shop       orders.Shop
	wa, sms    *fake.Channel
	mail       *mails
	links      messaging.ConfirmLinks
	store      tenant.StoreID
	ctx        context.Context
	variant    ulid.ID
}

func newPlatform(t *testing.T, quota int64) platform {
	t.Helper()
	ctx := t.Context()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pool, err := pgxpool.New(ctx, infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	owner, err := pgxpool.New(ctx, infra.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
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
	limiter, err := ratelimit.New(rdb, "msg-acc-"+strings.ToLower(ids.New().String()))
	if err != nil {
		t.Fatal(err)
	}
	hooks, err := webhooks.New(webhooks.Deps{Pool: pool, IDs: ids, Limiter: limiter, Log: log,
		ClientIP: func(context.Context) (netip.Addr, bool) { return netip.MustParseAddr("198.51.100.31"), true }})
	if err != nil {
		t.Fatal(err)
	}
	ent, err := entitlements.New(entitlements.Deps{Pool: pool, Config: config.Entitlements{MessagesPerMonth: quota}})
	if err != nil {
		t.Fatal(err)
	}
	mail := &mails{}
	msg, err := messaging.New(messaging.Deps{Pool: pool, IDs: ids, Secrets: config.Secrets{Keyring: keyring, OTPPepper: []byte(strings.Repeat("p", 32))},
		Config: config.Messaging{FakeChannels: true}, Quota: ent.Entitlements(), Jobs: ins, PublicURL: "https://app.example.com",
		Inbox: hooks.Inbox(), Log: log, Orders: ord.Fulfilment(),
		Stores: func(context.Context) (messaging.StoreInfo, error) {
			return messaging.StoreInfo{Name: "سوق", Locale: "ar", Host: "souq.iftahonline.test"}, nil
		},
		Contacts: func(context.Context) ([]string, error) { return []string{"owner@example.com"}, nil },
		Mailer:   mail, DashboardURL: "https://app.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range msg.PlatformProviders() {
		if err = hooks.RegisterPlatform(p); err != nil {
			t.Fatal(err)
		}
	}
	if err = bus.Subscribe(orders.EventOrderPlaced, messaging.ConfirmOrdersSubscriber, msg.OrderPlacedHandler()); err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	if err = river.AddWorkerSafely(workers, bus.Worker()); err != nil {
		t.Fatal(err)
	}
	if err = msg.RegisterWorkers(workers); err != nil {
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
		_, execErr := tx.Exec(sctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'سوق', 'EGP', 'ar', 'Africa/Cairo')`,
			store.ID, "mac-"+strings.ToLower(store.String()[14:]))
		return execErr
	}); err != nil {
		t.Fatal(err)
	}
	resp := problem.NewResponder(log)
	scope := testkit.FakeStoreScope(resp, map[string]testkit.FakeMember{"owner": {UserID: ids.New(), Roles: map[tenant.StoreID]authz.Role{store: authz.RoleOwner}}})
	api := http.NewServeMux()
	cat.Mount(api, pol, scope, resp)
	ord.Mount(api, pol, scope, resp)
	msg.Mount(api, pol, scope, resp)
	public := http.NewServeMux()
	hooks.Mount(public, resp)
	wa, sms := msg.Fakes()
	links, err := messaging.NewConfirmLinks(messaging.LinksDeps{Pool: pool, IDs: ids, Jobs: ins, Orders: ord.Fulfilment(), Log: log})
	if err != nil {
		t.Fatal(err)
	}
	p := platform{owner: owner, app: pool, api: api, public: public, shop: ord.Shop(), wa: wa, sms: sms, mail: mail, links: links,
		store: store, ctx: sctx}
	prod := p.call(t, http.MethodPost, "/products", `{"title":"ساعة","slug":"watch","status":"active","variants":[{"price_minor":45000,"on_hand":50}]}`, http.StatusCreated)
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

// place places a cash-on-delivery order from phone.
func (p platform) place(t *testing.T, phoneNumber string) string {
	t.Helper()
	placed, err := p.shop.PlaceOrder(p.ctx, orders.NewOrder{Source: orders.SourceStorefront, PaymentMethod: orders.PaymentCOD,
		Name: "منى أحمد", Phone: phoneNumber, Governorate: "EG-C", City: "مدينة نصر", Address: "12 شارع عباس العقاد",
		Lines: []orders.LineInput{{VariantID: p.variant, Qty: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	return placed.Order.ID.String()
}

// sent is what ch sent for this test's store (River runs the jobs of
// every test's store; another test's leftovers may go through ch).
func (p platform) sent(ch *fake.Channel) []channel.Message {
	var out []channel.Message
	for _, m := range ch.Sent() {
		if strings.Contains(m.Ref, p.store.String()) {
			out = append(out, m)
		}
	}
	return out
}

// sentTo waits for the store's n-th message on ch and returns it.
func (p platform) sentTo(t *testing.T, ch *fake.Channel, n int) channel.Message {
	t.Helper()
	eventually(t, "the confirmation message", func() bool { return len(p.sent(ch)) >= n })
	return p.sent(ch)[n-1]
}

// tap delivers the customer's tap on a button through the platform's
// webhook URL, as WhatsApp would.
func (p platform) tap(t *testing.T, id, payload string) {
	t.Helper()
	w := p.wa.Webhook(channel.Event{Kind: channel.ButtonEvent, ID: id, Payload: payload})
	rq := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/webhooks/"+p.wa.Name(), strings.NewReader(string(w.Body)))
	for k, vs := range w.Header {
		rq.Header[k] = vs
	}
	rq.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.public.ServeHTTP(rec, rq)
	if rec.Code != http.StatusOK {
		t.Fatalf("webhook = %d %s", rec.Code, rec.Body.String())
	}
}

func (p platform) order(t *testing.T, id string) (status string, actors []string) {
	t.Helper()
	o := p.call(t, http.MethodGet, "/orders/"+id, "", http.StatusOK)
	for _, e := range o["events"].([]any) {
		ev := e.(map[string]any)
		if a, ok := ev["actor"].(string); ok && ev["to"] != nil {
			actors = append(actors, a+">"+ev["to"].(string))
		}
	}
	return o["order"].(map[string]any)["status"].(string), actors
}

// confirmation is an order's confirmation status ("" while it has none).
func (p platform) confirmation(t *testing.T, order string) string {
	t.Helper()
	rq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/stores/"+p.store.String()+"/messaging/confirmations/"+order, nil)
	rq.Header.Set("Authorization", "Bearer owner")
	rec := httptest.NewRecorder()
	p.api.ServeHTTP(rec, rq)
	var c struct{ Status string }
	_ = json.Unmarshal(rec.Body.Bytes(), &c) //nolint:errcheck // a 404 has no status
	return c.Status
}

func (p platform) notices(t *testing.T) []string {
	t.Helper()
	var kinds []string
	for _, n := range p.call(t, http.MethodGet, "/messaging/notifications", "", http.StatusOK)["items"].([]any) {
		kinds = append(kinds, n.(map[string]any)["kind"].(string))
	}
	return kinds
}

// TestConfirmationRoundTrip is P6's acceptance for the confirmation
// flow, end to end with the fake channel: a cash-on-delivery order is
// placed; the customer gets the template with Confirm and Cancel; their
// tap comes back through the platform's webhook URL and moves the order;
// the merchant is told. Replays change nothing.
func TestConfirmationRoundTrip(t *testing.T) {
	p := newPlatform(t, 100)

	// Confirm.
	confirmed := p.place(t, "01012345678")
	m := p.sentTo(t, p.wa, 1)
	if m.Template != "order_confirmation_v1" || len(m.Buttons) != 2 || m.Params[1] != "سوق" || m.Params[2] != orders.CodeOf(mustID(t, confirmed)) {
		t.Fatalf("message = %+v", m)
	}
	c := p.call(t, http.MethodGet, "/messaging/confirmations/"+confirmed, "", http.StatusOK)
	if c["status"] != "pending" || c["message_id"] == nil {
		t.Fatalf("confirmation = %v", c)
	}
	for range 3 { // WhatsApp retries; once counts
		p.tap(t, "wamid.in.confirm", m.Buttons[0].Payload)
	}
	eventually(t, "the order to be confirmed", func() bool { got, _ := p.order(t, confirmed); return got == "confirmed" })
	time.Sleep(300 * time.Millisecond) // any duplicate job (there must be none) would run now
	st, actors := p.order(t, confirmed)
	if st != "confirmed" || strings.Count(strings.Join(actors, ","), "customer:whatsapp>confirmed") != 1 {
		t.Fatalf("order = %s %v", st, actors)
	}
	if c = p.call(t, http.MethodGet, "/messaging/confirmations/"+confirmed, "", http.StatusOK); c["status"] != "confirmed" || c["answered_by"] != "customer:whatsapp" {
		t.Fatalf("confirmation after = %v", c)
	}
	// The other button afterwards changes nothing.
	p.tap(t, "wamid.in.cancel-late", m.Buttons[1].Payload)
	time.Sleep(300 * time.Millisecond)
	if st, _ = p.order(t, confirmed); st != "confirmed" {
		t.Fatalf("late cancel moved the order: %s", st)
	}

	// Cancel.
	cancelled := p.place(t, "01112345678")
	m = p.sentTo(t, p.wa, 2)
	p.tap(t, "wamid.in.cancel", m.Buttons[1].Payload)
	eventually(t, "the order to be cancelled", func() bool { got, _ := p.order(t, cancelled); return got == "cancelled" })
	if _, actors = p.order(t, cancelled); !strings.Contains(strings.Join(actors, ","), "customer:whatsapp>cancelled") {
		t.Fatalf("cancel actors = %v", actors)
	}

	// The merchant moved the order first: the answer changes nothing.
	byMerchant := p.place(t, "01212345678")
	m = p.sentTo(t, p.wa, 3)
	p.call(t, http.MethodPost, "/orders/"+byMerchant+"/transitions", `{"status":"cancelled","note":"duplicate"}`, http.StatusOK)
	p.tap(t, "wamid.in.late", m.Buttons[0].Payload)
	eventually(t, "the confirmation to go stale", func() bool {
		return p.call(t, http.MethodGet, "/messaging/confirmations/"+byMerchant, "", http.StatusOK)["status"] == "stale"
	})
	if st, _ = p.order(t, byMerchant); st != "cancelled" {
		t.Fatalf("stale answer moved the order: %s", st)
	}

	notices := strings.Join(p.notices(t), ",")
	if !strings.Contains(notices, "order_confirmed") || !strings.Contains(notices, "order_cancelled") || strings.Count(notices, "order_") != 2 {
		t.Fatalf("notices = %s", notices)
	}
	if len(p.mail.all()) != 0 {
		t.Fatalf("orders' notices were emailed: %+v", p.mail.all())
	}
}

func mustID(t *testing.T, s string) ulid.ID {
	t.Helper()
	id, err := ulid.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// runDeadline makes an order's auto-cancel job due now.
func (p platform) runDeadline(t *testing.T, order string) {
	t.Helper()
	tag, err := p.owner.Exec(t.Context(), `UPDATE river_job SET scheduled_at = now(), state = 'available' WHERE kind = 'messaging.auto_cancel_order' AND args->>'order_id' = $1`, order)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("deadline job of %s: %v (%d rows)", order, err, tag.RowsAffected())
	}
}

// TestAutoCancel: an unanswered order whose customer got the message is
// cancelled at its deadline; one whose message never reached the
// customer is left to the merchant, who is told.
func TestAutoCancel(t *testing.T) {
	p := newPlatform(t, 100)
	p.call(t, http.MethodPut, "/messaging/settings", `{"confirm_cod":true,"auto_cancel_hours":2}`, http.StatusOK)

	silent := p.place(t, "01012345678")
	p.sentTo(t, p.wa, 1)
	c := p.call(t, http.MethodGet, "/messaging/confirmations/"+silent, "", http.StatusOK)
	expires, err := time.Parse(time.RFC3339Nano, c["expires_at"].(string))
	if err != nil || time.Until(expires) < 100*time.Minute || time.Until(expires) > 2*time.Hour {
		t.Fatalf("expires_at = %v (%v)", c["expires_at"], err)
	}
	p.runDeadline(t, silent)
	eventually(t, "the unanswered order to be cancelled", func() bool { st, _ := p.order(t, silent); return st == "cancelled" })
	if _, actors := p.order(t, silent); !strings.Contains(strings.Join(actors, ","), "system:auto_cancel>cancelled") {
		t.Fatalf("actors = %v", actors)
	}

	// Unreachable on WhatsApp and SMS: the message fails, the order stays.
	unreached := "+201099999999"
	p.wa.Unreachable(unreached)
	p.sms.Unreachable(unreached)
	lost := p.place(t, "01099999999")
	eventually(t, "the confirmation to open", func() bool { return p.confirmation(t, lost) == "pending" })
	p.runDeadline(t, lost)
	eventually(t, "the confirmation to be unreached", func() bool {
		return p.confirmation(t, lost) == "unreached"
	})
	if st, _ := p.order(t, lost); st != "pending" {
		t.Fatalf("an unreached customer's order moved: %s", st)
	}
	notices := strings.Join(p.notices(t), ",")
	if !strings.Contains(notices, "order_auto_cancelled") || !strings.Contains(notices, "confirmation_unreached") {
		t.Fatalf("notices = %s", notices)
	}
}

// TestQuotaExceeded is P6's acceptance for quotas: past the store's
// quota the message is not sent, the merchant is warned (dashboard and
// email, once), and the order is unaffected.
func TestQuotaExceeded(t *testing.T) {
	p := newPlatform(t, 1)
	first := p.place(t, "01012345678")
	p.sentTo(t, p.wa, 1)
	eventually(t, "the 100% warning", func() bool { return strings.Contains(strings.Join(p.notices(t), ","), "quota_reached") })

	over := p.place(t, "01112345678")
	eventually(t, "the second confirmation to be skipped", func() bool {
		return p.confirmation(t, over) == "skipped"
	})
	time.Sleep(300 * time.Millisecond)
	if n := len(p.sent(p.wa)) + len(p.sent(p.sms)); n != 1 {
		t.Fatalf("%d messages sent, want 1", n)
	}
	st, actors := p.order(t, over)
	if st != "pending" || len(actors) > 1 {
		t.Fatalf("order over the quota = %s %v", st, actors)
	}
	if st, _ = p.order(t, first); st != "pending" {
		t.Fatalf("first order = %s", st)
	}
	var skipped map[string]any
	for _, it := range p.call(t, http.MethodGet, "/messages?order_id="+over, "", http.StatusOK)["items"].([]any) {
		skipped = it.(map[string]any)
	}
	if skipped == nil || skipped["status"] != "skipped_quota" {
		t.Fatalf("log of the skipped message = %v", skipped)
	}
	u := p.call(t, http.MethodGet, "/messaging/usage", "", http.StatusOK)
	if u["used"] != float64(1) || u["level"] != "reached" {
		t.Fatalf("usage = %v", u)
	}
	// One warning, in the dashboard and by email, however many orders.
	p.place(t, "01212345678")
	time.Sleep(500 * time.Millisecond)
	if n := strings.Count(strings.Join(p.notices(t), ","), "quota_reached"); n != 1 {
		t.Fatalf("%d quota warnings", n)
	}
	eventually(t, "the warning email", func() bool { return len(p.mail.all()) == 1 })
	if e := p.mail.all()[0]; e.To != "owner@example.com" || !strings.Contains(e.Subject, "نفدت") {
		t.Fatalf("email = %+v", e)
	}
	// No deadline job for a skipped confirmation.
	var n int
	if err := p.owner.QueryRow(t.Context(), `SELECT count(*) FROM river_job WHERE kind = 'messaging.auto_cancel_order' AND args->>'order_id' = $1`, over).Scan(&n); err != nil || n != 0 {
		t.Fatalf("deadline jobs for a skipped order: %d %v", n, err)
	}
}

// TestCardOrdersAndOptOut: card orders are not asked (the gateway
// confirms them), nor are a store's that turned confirmation off.
func TestCardOrdersAndOptOut(t *testing.T) {
	p := newPlatform(t, 100)
	p.call(t, http.MethodPut, "/messaging/settings", `{"confirm_cod":false,"auto_cancel_hours":24}`, http.StatusOK)
	off := p.place(t, "01012345678")
	time.Sleep(500 * time.Millisecond)
	p.call(t, http.MethodGet, "/messaging/confirmations/"+off, "", http.StatusNotFound)
	if len(p.sent(p.wa)) != 0 {
		t.Fatal("a message was sent with confirmation off")
	}
}

// TestSMSRoundTrip: a customer not on WhatsApp gets the SMS with the
// link; answering on the link's page (the storefront's side) confirms
// the order on the customer's behalf.
func TestSMSRoundTrip(t *testing.T) {
	p := newPlatform(t, 100)
	p.wa.Unreachable("+201512345678")
	id := p.place(t, "01512345678")
	m := p.sentTo(t, p.sms, 1)
	_, after, ok := strings.Cut(m.Text, "https://souq.iftahonline.test/ar/o/")
	if !ok || len(after) < 22 {
		t.Fatalf("SMS = %q", m.Text)
	}
	token := after[:22]
	v, err := p.links.LinkConfirmation(p.ctx, token)
	if err != nil || v.Status != messaging.ConfirmationPending || v.OrderCode != orders.CodeOf(mustID(t, id)) || v.FirstName != "منى" {
		t.Fatalf("link = %+v, %v", v, err)
	}
	if v, err = p.links.AnswerLink(p.ctx, token, messaging.Confirm); err != nil || v.Status != messaging.ConfirmationConfirmed {
		t.Fatalf("answer = %+v, %v", v, err)
	}
	st, actors := p.order(t, id)
	if st != "confirmed" || !strings.Contains(strings.Join(actors, ","), "customer:sms>confirmed") {
		t.Fatalf("order = %s %v", st, actors)
	}
	var fallback map[string]any
	for _, it := range p.call(t, http.MethodGet, "/messages?order_id="+id, "", http.StatusOK)["items"].([]any) {
		fallback = it.(map[string]any)
	}
	if fallback["channel"] != "sms" || fallback["fallback"] != true {
		t.Fatalf("log = %v", fallback)
	}
}
