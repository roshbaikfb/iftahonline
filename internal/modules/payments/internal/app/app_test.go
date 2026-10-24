package app_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway/fake"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/gateway/paymob"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments/internal/paytest"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/webhookstest"
	"github.com/roshbaikfb/iftahonline/internal/platform/envelope"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
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

type fixture struct {
	t          *testing.T
	pool       *pgxpool.Pool
	ids        *ulid.Generator
	gw         *fake.Gateway
	orders     *paytest.Orders
	jobs       *testkit.Jobs
	inbox      *webhookstest.Inbox
	svc        *app.Service
	ctxA, ctxB context.Context
	conn       domain.Connection
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	f := fixture{t: t, pool: pool, ids: ulid.NewGenerator(), gw: fake.New(), orders: paytest.NewOrders(),
		jobs: testkit.NewJobs(), inbox: webhookstest.NewInbox()}
	f.svc = f.service(f.gw)
	f.ctxA, f.ctxB = f.mkStore(t), f.mkStore(t)
	if f.conn, err = f.svc.Connect(f.ctxA, app.ConnectInput{Gateway: fake.Name, APIKey: apiKey, WebhookSecret: hookSecret}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f fixture) service(g gateway.Gateway) *app.Service {
	k, err := secretbox.ParseKeyring("1:ZGV2LW9ubHktc2VjcmV0Ym94LWtleS0zMi1ieXRlcyE=") // public dev key
	if err != nil {
		f.t.Fatal(err)
	}
	sealer, err := envelope.New(envelope.FromKeyring(k))
	if err != nil {
		f.t.Fatal(err)
	}
	return app.NewService(app.Deps{DB: f.pool, IDs: f.ids, Gateways: app.Gateways{fake.Name: g}, Sealer: sealer,
		PublicURL: "https://app.example.com", Orders: f.orders, Jobs: f.jobs, Inbox: f.inbox})
}

func (f fixture) mkStore(t *testing.T) context.Context {
	t.Helper()
	id := tenant.StoreID{ID: f.ids.New()}
	ctx := tenant.WithStore(t.Context(), id)
	if err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
			id.ID, "pay-"+strings.ToLower(id.String()[14:]))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return ctx
}

// checkout starts a card order's payment and returns it.
func (f fixture) checkout(t *testing.T, order ulid.ID) domain.Payment {
	t.Helper()
	c, err := f.svc.StartCheckout(f.ctxA, order, "https://shop.example.com/thank-you/x")
	if err != nil {
		t.Fatal(err)
	}
	p, _, err := f.svc.Payment(f.ctxA, c.PaymentID)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// deliver sends the fake's webhook for a payment through the provider
// and runs the job, as the inbox and the worker would.
func (f fixture) deliver(t *testing.T, p domain.Payment) ulid.ID {
	ctx := f.ctxA
	t.Helper()
	body, sig, err := f.gw.Webhook(apiKey, hookSecret, p.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	h := http.Header{}
	h.Set(fake.SignatureHeader, sig)
	if _, err = f.svc.WebhookProviders()[0].Verify(ctx, f.conn.ID, webhooks.Request{Header: h, Body: body}); err != nil {
		t.Fatal(err)
	}
	r := f.inbox.Put(fake.Name, body)
	if err = f.svc.ProcessWebhook(ctx, r); err != nil {
		t.Fatal(err)
	}
	return r
}

func isField(err error, field, code string) bool {
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindValidation {
		return false
	}
	for _, fe := range e.Fields {
		if fe.Field == field && fe.Code == code {
			return true
		}
	}
	return false
}

func TestConnect(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	if _, err := f.svc.Connect(f.ctxA, app.ConnectInput{Gateway: fake.Name, APIKey: apiKey, WebhookSecret: hookSecret}); !errors.Is(err, app.ErrGatewayConnected) {
		t.Errorf("second gateway: %v", err)
	}
	if _, err := f.svc.Connect(f.ctxB, app.ConnectInput{Gateway: fake.Name, APIKey: fake.InvalidKey, WebhookSecret: "s"}); !isField(err, "secret_key", "refused") {
		t.Errorf("refused keys: %v", err)
	}
	if _, err := f.svc.Connect(f.ctxB, app.ConnectInput{Gateway: "stripe", APIKey: "k", WebhookSecret: "s"}); !isField(err, "gateway", "unknown") {
		t.Errorf("unknown gateway: %v", err)
	}
	if _, err := f.svc.Connect(f.ctxB, app.ConnectInput{Gateway: fake.Name, APIKey: "k"}); !isField(err, "webhook_secret", "invalid") {
		t.Errorf("no webhook secret: %v", err)
	}
	withPaymob := app.NewService(app.Deps{DB: f.pool, IDs: f.ids, Gateways: app.Gateways{paymob.Name: paymob.New(paymob.Options{BaseURL: "http://127.0.0.1:1"})},
		Orders: f.orders, Jobs: f.jobs})
	if _, err := withPaymob.Connect(f.ctxB, app.ConnectInput{Gateway: paymob.Name, SecretKey: "sk", PublicKey: "pk", APIKey: "k", WebhookSecret: "s",
		IntegrationIDs: []int{0, -3}}); !isField(err, "integration_ids", "required") {
		t.Errorf("Paymob without integration IDs: %v", err)
	}
	var blob []byte
	if err := postgres.InTenantTx(f.ctxA, f.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(f.ctxA, `SELECT credentials FROM payment_connections WHERE id = $1`, f.conn.ID).Scan(&blob)
	}); err != nil || strings.Contains(string(blob), apiKey) || strings.Contains(string(blob), hookSecret) {
		t.Fatalf("keys stored in the clear (%v)", err)
	}
	if on, err := f.svc.CardEnabled(f.ctxA); err != nil || !on {
		t.Fatalf("A takes cards: %v %v", on, err)
	}
	if on, err := f.svc.CardEnabled(f.ctxB); err != nil || on {
		t.Fatalf("B takes cards: %v %v", on, err)
	}
	if err := f.svc.Disconnect(f.ctxB, f.conn.ID); !errors.Is(err, app.ErrConnectionNotFound) {
		t.Fatalf("B disconnects A: %v", err)
	}
	if list, err := f.svc.Connections(f.ctxA); err != nil || len(list) != 1 {
		t.Fatalf("A's connections: %+v %v", list, err)
	}
	if err := f.svc.Disconnect(f.ctxA, f.conn.ID); err != nil {
		t.Fatal(err)
	}
	if on, err := f.svc.CardEnabled(f.ctxA); err != nil || on {
		t.Fatalf("after disconnect: %v %v", on, err)
	}
}

// TestCheckout: a card order's shopper gets the gateway's page, with the
// expiry scheduled an hour after the order; coming back reuses the page.
func TestCheckout(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	order := f.orders.Add(t, orders.PaymentCard, 0)
	c, err := f.svc.StartCheckout(f.ctxA, order, "https://shop.example.com/thank-you/x")
	if err != nil || !strings.HasPrefix(c.URL, "https://pay.fake.test/checkout/") {
		t.Fatalf("checkout %+v %v", c, err)
	}
	p, _, err := f.svc.Payment(f.ctxA, c.PaymentID)
	if err != nil || p.Status != domain.StatusPending || p.Amount.Amount() != 52000 || p.CheckoutURL != c.URL {
		t.Fatalf("payment %+v %v", p, err)
	}
	if kinds := f.jobs.Kinds(); len(kinds) != 1 || kinds[0] != "payments.expire_payment" ||
		f.jobs.Opts[0].ScheduledAt.Sub(f.orders.Order(order).CreatedAt) < app.ExpireAfter-time.Second {
		t.Fatalf("expiry job %v %+v", kinds, f.jobs.Opts)
	}
	again, err := f.svc.StartCheckout(f.ctxA, order, "https://shop.example.com/thank-you/x")
	if err != nil || again.PaymentID != c.PaymentID {
		t.Fatalf("coming back: %+v %v, want the same payment", again, err)
	}

	cod := f.orders.Add(t, orders.PaymentCOD, 0)
	if _, err = f.svc.StartCheckout(f.ctxA, cod, ""); !errors.Is(err, app.ErrNotPayable) {
		t.Errorf("COD order: %v", err)
	}
	if _, err = f.svc.StartCheckout(f.ctxA, ulid.NewGenerator().New(), ""); !errors.Is(err, app.ErrNotPayable) {
		t.Errorf("unknown order: %v", err)
	}
	if _, err = f.svc.StartCheckout(f.ctxB, f.orders.Add(t, orders.PaymentCard, 0), ""); !errors.Is(err, app.ErrCardUnavailable) {
		t.Errorf("a store without a gateway: %v", err)
	}
}

// downGateway cannot create payments.
type downGateway struct{ *fake.Gateway }

func (downGateway) CreatePayment(context.Context, gateway.Credentials, gateway.PaymentRequest) (gateway.Checkout, error) {
	return gateway.Checkout{}, errors.New("gateway timeout")
}

func TestCheckoutGatewayDown(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	svc := f.service(downGateway{f.gw})
	order := f.orders.Add(t, orders.PaymentCard, 0)
	if _, err := svc.StartCheckout(f.ctxA, order, ""); errs.KindOf(err) != errs.KindUnavailable {
		t.Fatalf("gateway down: %v", err)
	}
	ps, err := svc.OrderPayments(f.ctxA, order)
	if err != nil || len(ps) != 1 || ps[0].Status != domain.StatusFailed || ps[0].Failure == "" {
		t.Fatalf("the failed attempt: %+v %v", ps, err)
	}
}

// TestPaidByWebhook: the shopper pays; the webhook (re-queried) marks the
// payment and its order paid and confirms the order; replays change
// nothing; a forged webhook is refused.
func TestPaidByWebhook(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	order := f.orders.Add(t, orders.PaymentCard, 0)
	p := f.checkout(t, order)
	if _, err := f.gw.Pay(apiKey, p.ID.String()); err != nil {
		t.Fatal(err)
	}
	r := f.deliver(t, p)
	got, _, err := f.svc.Payment(f.ctxA, p.ID)
	if err != nil || got.Status != domain.StatusPaid || got.TransactionID == "" || f.inbox.Outcome(r) != "updated" {
		t.Fatalf("after the webhook: %+v %v %q", got, err, f.inbox.Outcome(r))
	}
	o := f.orders.Order(order)
	if o.PaymentStatus != orders.PaymentPaid || o.Status != orders.StatusConfirmed ||
		strings.Join(f.orders.History(), "; ") != "payment paid by gateway:fakepay; confirmed by gateway:fakepay" {
		t.Fatalf("order %+v, history %v", o, f.orders.History())
	}
	for range 4 {
		if r2 := f.deliver(t, p); f.inbox.Outcome(r2) != "unchanged" {
			t.Fatalf("replay outcome %q", f.inbox.Outcome(r2))
		}
	}
	if len(f.orders.History()) != 2 {
		t.Fatalf("replays moved the order: %v", f.orders.History())
	}

	prov := f.svc.WebhookProviders()[0]
	body, _, err := f.gw.Webhook(apiKey, hookSecret, p.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	h := http.Header{}
	h.Set(fake.SignatureHeader, strings.Repeat("ab", 32))
	if _, err = prov.Verify(f.ctxA, f.conn.ID, webhooks.Request{Header: h, Body: body}); !errors.Is(err, webhooks.ErrForged) {
		t.Fatalf("forged: %v", err)
	}
	if _, err = prov.Verify(f.ctxB, f.conn.ID, webhooks.Request{Header: h, Body: body}); !errors.Is(err, webhooks.ErrUnknownConnection) {
		t.Fatalf("A's connection under B: %v", err)
	}
	unknown := f.inbox.Put(fake.Name, []byte(`{"event_id":"e","reference":"not-ours"}`))
	if err = f.svc.ProcessWebhook(f.ctxA, unknown); err != nil || f.inbox.Outcome(unknown) != "unknown_payment" {
		t.Fatalf("unknown payment: %v %q", err, f.inbox.Outcome(unknown))
	}
	bad := f.inbox.Put(fake.Name, []byte(`nope`))
	if err = f.svc.ProcessWebhook(f.ctxA, bad); err != nil || f.inbox.Outcome(bad) != "unreadable" {
		t.Fatalf("unreadable: %v %q", err, f.inbox.Outcome(bad))
	}
	// B's worker never sees A's payment.
	other := f.inbox.Put(fake.Name, body)
	if err = f.svc.ProcessWebhook(f.ctxB, other); err != nil || f.inbox.Outcome(other) != "unknown_payment" {
		t.Fatalf("A's payment in B's webhook: %v %q", err, f.inbox.Outcome(other))
	}
	if _, _, err = f.svc.Payment(f.ctxB, p.ID); !errors.Is(err, app.ErrPaymentNotFound) {
		t.Fatalf("B reads A's payment: %v", err)
	}
}

// TestMoneyTakenTwice: a second payment of a paid order, or a payment of
// a cancelled order, is refunded at once.
func TestMoneyTakenTwice(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	order := f.orders.Add(t, orders.PaymentCard, 0)
	first := f.checkout(t, order)
	// The shopper opens the page again later (a new payment) and pays both.
	f.svc.SetClock(func() time.Time { return time.Now().Add(app.ReuseCheckoutFor + time.Minute) })
	second := f.checkout(t, order)
	f.svc.SetClock(time.Now)
	if second.ID == first.ID {
		t.Fatal("expected a second payment")
	}
	for _, p := range []domain.Payment{first, second} {
		if _, err := f.gw.Pay(apiKey, p.ID.String()); err != nil {
			t.Fatal(err)
		}
		f.deliver(t, p)
	}
	_, refunds, err := f.svc.Payment(f.ctxA, second.ID)
	if err != nil || len(refunds) != 1 || refunds[0].Amount.Amount() != 52000 || !strings.Contains(refunds[0].Reason, "already paid") {
		t.Fatalf("the second payment's refund: %+v %v", refunds, err)
	}

	cancelled := f.orders.Add(t, orders.PaymentCard, 0)
	late := f.checkout(t, cancelled)
	f.orders.SetStatus(cancelled, orders.StatusCancelled)
	if _, err = f.gw.Pay(apiKey, late.ID.String()); err != nil {
		t.Fatal(err)
	}
	f.deliver(t, late)
	if _, refunds, err = f.svc.Payment(f.ctxA, late.ID); err != nil || len(refunds) != 1 || !strings.Contains(refunds[0].Reason, "cancelled") {
		t.Fatalf("a cancelled order's payment: %+v %v", refunds, err)
	}
	if f.orders.Order(cancelled).PaymentStatus == orders.PaymentPaid {
		t.Fatal("a cancelled order was marked paid")
	}
}

// TestExpiry: an hour without payment expires the payment and cancels
// the order; a payment made at the last moment is caught by the final
// inquiry; an open newer attempt keeps the order.
func TestExpiry(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	unpaid := f.orders.Add(t, orders.PaymentCard, 2*time.Hour)
	p := f.checkout(t, unpaid)
	if err := f.svc.ExpirePayment(f.ctxA, p.ID); err != nil {
		t.Fatal(err)
	}
	got, _, err := f.svc.Payment(f.ctxA, p.ID)
	if err != nil || got.Status != domain.StatusExpired || f.orders.Order(unpaid).Status != orders.StatusCancelled ||
		!strings.Contains(strings.Join(f.orders.History(), ";"), "cancelled by system:payment_expiry") {
		t.Fatalf("expired: %+v %v, order %+v", got, err, f.orders.Order(unpaid))
	}

	lastMinute := f.orders.Add(t, orders.PaymentCard, 2*time.Hour)
	q := f.checkout(t, lastMinute)
	if _, err = f.gw.Pay(apiKey, q.ID.String()); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.ExpirePayment(f.ctxA, q.ID); err != nil {
		t.Fatal(err)
	}
	if got, _, err = f.svc.Payment(f.ctxA, q.ID); err != nil || got.Status != domain.StatusPaid || f.orders.Order(lastMinute).Status != orders.StatusConfirmed {
		t.Fatalf("paid at the last minute: %+v %v", got, err)
	}

	retried := f.orders.Add(t, orders.PaymentCard, 2*time.Hour)
	old := f.checkout(t, retried)
	f.svc.SetClock(func() time.Time { return time.Now().Add(app.ReuseCheckoutFor + time.Minute) })
	f.checkout(t, retried) // a newer, still open attempt
	f.svc.SetClock(time.Now)
	if err = f.svc.ExpirePayment(f.ctxA, old.ID); err != nil {
		t.Fatal(err)
	}
	if f.orders.Order(retried).Status != orders.StatusPending {
		t.Fatalf("an order with an open attempt was cancelled: %+v", f.orders.Order(retried))
	}
	if err = f.svc.ExpirePayment(f.ctxA, ulid.NewGenerator().New()); err != nil {
		t.Fatalf("a missing payment: %v", err)
	}
}

// TestRefunds: partial then full refunds through the job; the payment
// and the order follow the gateway; what is asked is reserved; a retried
// job never refunds twice.
func TestRefunds(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	order := f.orders.Add(t, orders.PaymentCard, 0)
	p := f.checkout(t, order)
	if _, err := f.svc.Refund(f.ctxA, p.ID, 100, ""); !errors.Is(err, app.ErrNotRefundable) {
		t.Fatalf("refund before paying: %v", err)
	}
	if _, err := f.gw.Pay(apiKey, p.ID.String()); err != nil {
		t.Fatal(err)
	}
	f.deliver(t, p)

	r, err := f.svc.Refund(f.ctxA, p.ID, 20000, "  the parcel came back damaged ")
	if err != nil || r.Status != domain.RefundRequested || r.Reason != "the parcel came back damaged" {
		t.Fatalf("refund %+v %v", r, err)
	}
	if _, err = f.svc.Refund(f.ctxA, p.ID, 32001, ""); !isField(err, "amount_minor", "range") {
		t.Fatalf("over what is left with one refund requested: %v", err)
	}
	for range 2 { // the second run finds the refund made
		if err = f.svc.RefundPayment(f.ctxA, r.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err = f.svc.SyncPayment(f.ctxA, p.ID); err != nil {
		t.Fatal(err)
	}
	got, refunds, err := f.svc.Payment(f.ctxA, p.ID)
	if err != nil || got.Status != domain.StatusPartiallyRefunded || got.Refunded.Amount() != 20000 || refunds[0].Status != domain.RefundSucceeded {
		t.Fatalf("after the refund: %+v %+v %v", got, refunds, err)
	}
	if st, stErr := f.gw.FetchStatus(f.ctxA, gateway.Credentials{APIKey: apiKey}, p.ID.String(), money.EGP); stErr != nil || st.Refunded.Amount() != 20000 {
		t.Fatalf("the gateway refunded %v (%v), want once", st.Refunded, stErr)
	}
	if f.orders.Order(order).PaymentStatus != orders.PaymentPartiallyRefunded {
		t.Fatalf("order %+v", f.orders.Order(order))
	}

	rest, err := f.svc.Refund(f.ctxA, p.ID, 32000, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.svc.RefundPayment(f.ctxA, rest.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.SyncPayment(f.ctxA, p.ID); err != nil {
		t.Fatal(err)
	}
	if got, _, err = f.svc.Payment(f.ctxA, p.ID); err != nil || got.Status != domain.StatusRefunded || f.orders.Order(order).PaymentStatus != orders.PaymentRefunded {
		t.Fatalf("fully refunded: %+v %v", got, err)
	}
	if _, err = f.svc.Refund(f.ctxB, p.ID, 1, ""); !errors.Is(err, app.ErrPaymentNotFound) {
		t.Fatalf("B refunds A's payment: %v", err)
	}
}

func TestPaymentsList(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	var ids []ulid.ID
	for range 3 {
		ids = append(ids, f.checkout(t, f.orders.Add(t, orders.PaymentCard, 0)).ID)
	}
	p, err := f.svc.Payments(f.ctxA, nil, 2)
	if err != nil || len(p.Items) != 2 || p.Next == nil || p.Items[0].ID != ids[2] {
		t.Fatalf("page 1: %+v %v", p, err)
	}
	if p, err = f.svc.Payments(f.ctxA, p.Next, 2); err != nil || len(p.Items) != 1 || p.Next != nil {
		t.Fatalf("page 2: %+v %v", p, err)
	}
	if p, err = f.svc.Payments(f.ctxB, nil, 0); err != nil || len(p.Items) != 0 {
		t.Fatalf("B's payments: %+v %v", p, err)
	}
	if _, err = f.svc.Payments(f.ctxA, nil, 500); !isField(err, "limit", "range") {
		t.Fatalf("limit: %v", err)
	}
	refreshed, err := f.svc.Refresh(f.ctxA, ids[0])
	if err != nil || refreshed.Status != domain.StatusPending {
		t.Fatalf("refresh: %+v %v", refreshed, err)
	}
	if err = f.svc.AddWorkers(river.NewWorkers()); err != nil {
		t.Fatal(err)
	}
}

// TestWorkers: the jobs a card order enqueues run through their workers:
// the accepted webhook, the refund, the sync, the expiry.
func TestWorkers(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	if names := f.svc.GatewayNames(); len(names) != 1 || names[0] != fake.Name {
		t.Fatalf("gateways %v", names)
	}
	prov := f.svc.WebhookProviders()[0]
	if prov.Name() != fake.Name {
		t.Fatalf("provider %q", prov.Name())
	}
	order := f.orders.Add(t, orders.PaymentCard, 0)
	p := f.checkout(t, order)
	if _, err := f.gw.Pay(apiKey, p.ID.String()); err != nil {
		t.Fatal(err)
	}
	body, _, err := f.gw.Webhook(apiKey, hookSecret, p.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	receipt := f.inbox.Put(fake.Name, body)
	if err = postgres.InTenantTx(f.ctxA, f.pool, func(tx pgx.Tx) error {
		return prov.AcceptTx(f.ctxA, tx, webhooks.Receipt{ID: receipt})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.Refund(f.ctxA, p.ID, 52000, ""); !errors.Is(err, app.ErrNotRefundable) {
		t.Fatalf("refund before the webhook ran: %v", err)
	}
	// Run every job enqueued so far, then the ones those enqueue, but
	// leave the expiry for last.
	done := 0
	drain := func() {
		for ; done < len(f.jobs.Args); done++ {
			if a := f.jobs.Args[done]; a.Kind() != "payments.expire_payment" {
				if runErr := f.svc.RunJob(f.ctxA, a); runErr != nil {
					t.Fatalf("%s: %v", a.Kind(), runErr)
				}
			}
		}
	}
	drain()
	if f.inbox.Outcome(receipt) != "updated" || f.orders.Order(order).Status != orders.StatusConfirmed {
		t.Fatalf("webhook job: %q, order %+v", f.inbox.Outcome(receipt), f.orders.Order(order))
	}
	if _, err = f.svc.Refund(f.ctxA, p.ID, 52000, "changed my mind"); err != nil {
		t.Fatal(err)
	}
	drain()
	got, refunds, err := f.svc.Payment(f.ctxA, p.ID)
	if err != nil || got.Status != domain.StatusRefunded || len(refunds) != 1 || refunds[0].Status != domain.RefundSucceeded ||
		f.orders.Order(order).PaymentStatus != orders.PaymentRefunded {
		t.Fatalf("refund and sync jobs: %+v %+v %v", got, refunds, err)
	}
	// The expiry of a paid (now refunded) payment keeps the order.
	if err = f.svc.RunJob(f.ctxA, f.jobs.Args[0]); err != nil {
		t.Fatal(err)
	}
	if f.orders.Order(order).Status != orders.StatusConfirmed {
		t.Fatalf("expiry cancelled a paid order: %+v", f.orders.Order(order))
	}
}

// slowGateway never answers status inquiries in time.
type slowGateway struct{ *fake.Gateway }

func (slowGateway) FetchStatus(ctx context.Context, _ gateway.Credentials, _ string, _ money.Currency) (gateway.Status, error) {
	<-ctx.Done()
	return gateway.Status{}, ctx.Err()
}

// TestRefreshOrder: the shopper is back; the order's open payment is
// asked about at once. A gateway that does not answer changes nothing.
func TestRefreshOrder(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	order := f.orders.Add(t, orders.PaymentCard, 0)
	p := f.checkout(t, order)
	if _, err := f.gw.Pay(apiKey, p.ID.String()); err != nil {
		t.Fatal(err)
	}
	if err := f.service(slowGateway{f.gw}).RefreshOrder(f.ctxA, order); err == nil {
		t.Fatal("a gateway that never answered: no error")
	}
	if got := f.orders.Order(order); got.PaymentStatus != orders.PaymentAwaiting {
		t.Fatalf("changed without the gateway's word: %+v", got)
	}
	if err := f.svc.RefreshOrder(f.ctxA, order); err != nil {
		t.Fatal(err)
	}
	if got := f.orders.Order(order); got.PaymentStatus != orders.PaymentPaid || got.Status != orders.StatusConfirmed {
		t.Fatalf("after refresh: %+v", got)
	}
	if err := f.svc.RefreshOrder(f.ctxA, order); err != nil { // nothing open: nothing asked
		t.Fatal(err)
	}
	if err := f.svc.RefreshOrder(f.ctxA, ulid.NewGenerator().New()); err != nil {
		t.Fatalf("an order without payments: %v", err)
	}
	if got := f.svc.ReturnURL(tenant.StoreID{ID: order}, order, "https://x.test/t"); !strings.HasPrefix(got, "https://app.example.com/v1/pay/") ||
		!strings.HasSuffix(got, "/return/aHR0cHM6Ly94LnRlc3QvdA") {
		t.Fatalf("ReturnURL = %q", got)
	}
}

// TestRefundsBeforeTheSync: two refunds of one payment run before the
// sync job brings the payment's new amounts: each must reach the
// gateway once. A refund whose job runs before an older one of the same
// payment waits for it.
func TestRefundsBeforeTheSync(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	order := f.orders.Add(t, orders.PaymentCard, 0)
	p := f.checkout(t, order)
	if _, err := f.gw.Pay(apiKey, p.ID.String()); err != nil {
		t.Fatal(err)
	}
	f.deliver(t, p)
	first, err := f.svc.Refund(f.ctxA, p.ID, 20000, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.svc.Refund(f.ctxA, p.ID, 15000, "")
	if err != nil {
		t.Fatal(err)
	}
	refunded := func() int64 {
		t.Helper()
		st, stErr := f.gw.FetchStatus(f.ctxA, gateway.Credentials{APIKey: apiKey}, p.ID.String(), money.EGP)
		if stErr != nil {
			t.Fatal(stErr)
		}
		return st.Refunded.Amount()
	}
	// The newer one first: it waits, asking nothing of the gateway.
	if err = f.svc.RefundPayment(f.ctxA, second.ID); !app.IsRefundWaiting(err) {
		t.Fatalf("the newer refund ran first: %v", err)
	}
	var snooze *river.JobSnoozeError
	if err = f.svc.RunJob(f.ctxA, app.RefundPaymentArgs{RefundID: second.ID}); !errors.As(err, &snooze) {
		t.Fatalf("the worker must snooze a waiting refund: %v", err)
	}
	if got := refunded(); got != 0 {
		t.Fatalf("the gateway refunded %d before the older refund", got)
	}
	for _, id := range []ulid.ID{first.ID, second.ID} {
		if err = f.svc.RefundPayment(f.ctxA, id); err != nil {
			t.Fatal(err)
		}
	}
	if got := refunded(); got != 35000 {
		t.Fatalf("the gateway refunded %d, want 35000 (each refund once)", got)
	}
	// Run again (a retried job): nothing more goes back.
	for _, id := range []ulid.ID{first.ID, second.ID} {
		if err = f.svc.RefundPayment(f.ctxA, id); err != nil {
			t.Fatal(err)
		}
	}
	if got := refunded(); got != 35000 {
		t.Fatalf("retried jobs refunded again: %d", got)
	}
	_, refunds, err := f.svc.Payment(f.ctxA, p.ID)
	if err != nil || len(refunds) != 2 || refunds[0].Status != domain.RefundSucceeded || refunds[1].Status != domain.RefundSucceeded {
		t.Fatalf("refunds %+v %v", refunds, err)
	}
}

// lostRefundGateway refunds, then loses the answer, once.
type lostRefundGateway struct {
	*fake.Gateway
	lost *bool
}

func (g lostRefundGateway) Refund(ctx context.Context, creds gateway.Credentials, txn string, amount money.Money) (string, error) {
	id, err := g.Gateway.Refund(ctx, creds, txn, amount)
	if err == nil && !*g.lost {
		*g.lost = true
		return "", errors.New("gateway timeout")
	}
	return id, err
}

// TestRefundAnswerLost: the gateway refunded but the job never heard;
// the retried job records it instead of refunding again.
func TestRefundAnswerLost(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	lost := false
	svc := f.service(lostRefundGateway{Gateway: f.gw, lost: &lost})
	order := f.orders.Add(t, orders.PaymentCard, 0)
	p := f.checkout(t, order)
	if _, err := f.gw.Pay(apiKey, p.ID.String()); err != nil {
		t.Fatal(err)
	}
	f.deliver(t, p)
	r, err := svc.Refund(f.ctxA, p.ID, 20000, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.RefundPayment(f.ctxA, r.ID); err == nil {
		t.Fatal("a lost answer must fail the job (River retries it)")
	}
	if err = svc.RefundPayment(f.ctxA, r.ID); err != nil {
		t.Fatal(err)
	}
	st, err := f.gw.FetchStatus(f.ctxA, gateway.Credentials{APIKey: apiKey}, p.ID.String(), money.EGP)
	if err != nil || st.Refunded.Amount() != 20000 {
		t.Fatalf("the gateway refunded %v (%v), want 20000 once", st.Refunded, err)
	}
	if _, refunds, _ := svc.Payment(f.ctxA, p.ID); refunds[0].Status != domain.RefundSucceeded {
		t.Fatalf("refund %+v", refunds[0])
	}
}

// TestCardOrderNeverPaid: a card order whose shopper never reached the
// gateway (the tab closed, a script, the gateway disconnected) is
// cancelled an hour after it was placed, so its stock comes back. The
// expiry is scheduled when the order is placed, not when a checkout
// starts.
func TestCardOrderNeverPaid(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	order := f.orders.Add(t, orders.PaymentCard, 2*time.Hour)
	if err := f.svc.WatchOrder(f.ctxA, order); err != nil {
		t.Fatal(err)
	}
	if kinds := f.jobs.Kinds(); len(kinds) != 1 || kinds[0] != "payments.expire_order" {
		t.Fatalf("jobs %v, want the order's expiry", kinds)
	}
	if err := f.svc.RunJob(f.ctxA, f.jobs.Args[0]); err != nil {
		t.Fatal(err)
	}
	if got := f.orders.Order(order); got.Status != orders.StatusCancelled {
		t.Fatalf("an unpaid card order was kept: %+v", got)
	}

	// A COD order is not watched; a paid card order is kept.
	cod := f.orders.Add(t, orders.PaymentCOD, 2*time.Hour)
	if err := f.svc.WatchOrder(f.ctxA, cod); err != nil || len(f.jobs.Kinds()) != 1 {
		t.Fatalf("a COD order was watched: %v %v", err, f.jobs.Kinds())
	}
	paid := f.orders.Add(t, orders.PaymentCard, 2*time.Hour)
	p := f.checkout(t, paid)
	if _, err := f.gw.Pay(apiKey, p.ID.String()); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.ExpireOrder(f.ctxA, paid); err != nil { // the last inquiry finds it paid
		t.Fatal(err)
	}
	if got := f.orders.Order(paid); got.Status != orders.StatusConfirmed || got.PaymentStatus != orders.PaymentPaid {
		t.Fatalf("a paid order: %+v", got)
	}
	if err := f.svc.ExpireOrder(f.ctxA, ulid.NewGenerator().New()); err != nil {
		t.Fatalf("an unknown order: %v", err)
	}
}
