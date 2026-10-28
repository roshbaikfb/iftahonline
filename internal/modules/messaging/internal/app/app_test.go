package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel/fake"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks/webhookstest"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/phone"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/secretbox"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

var buttonKey = domain.ButtonKey("0123456789abcdef0123456789abcdef")

// replies records the answers handed to the reply handler.
type replies struct {
	mu  sync.Mutex
	got []app.Reply
}

func (r *replies) HandleReplyTx(_ context.Context, _ pgx.Tx, rep app.Reply) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, rep)
	return nil
}

type fixture struct {
	svc     *app.Service
	pool    *pgxpool.Pool
	jobs    *testkit.Jobs
	inbox   *webhookstest.Inbox
	wa, sms *fake.Channel
	quota   entitlements.Entitlements
	replies *replies
	ids     *ulid.Generator
}

type options struct {
	number string // the platform's WhatsApp number (customer-started confirmation)
	limit  int64
	noWA   bool
	noSMS  bool
	chans  *app.Channels
}

func newFixture(t *testing.T, o options) fixture {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	keyring, err := secretbox.ParseKeyring("1:MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatal(err)
	}
	if o.limit == 0 {
		o.limit = 100
	}
	ent, err := entitlements.New(entitlements.Deps{Pool: pool, Config: config.Entitlements{MessagesPerMonth: o.limit}})
	if err != nil {
		t.Fatal(err)
	}
	f := fixture{pool: pool, jobs: testkit.NewJobs(), inbox: webhookstest.NewInbox(), quota: ent.Entitlements(), replies: &replies{}, ids: ulid.NewGenerator(),
		wa: fake.New("fake_whatsapp", []byte("wa-secret")), sms: fake.New("fake_sms", []byte("sms-secret"))}
	chans := app.Channels{WhatsApp: f.wa, SMS: f.sms}
	if o.noWA {
		chans.WhatsApp = nil
	}
	if o.noSMS {
		chans.SMS = nil
	}
	if o.chans != nil {
		chans = *o.chans
	}
	f.svc = app.NewService(app.Deps{DB: pool, IDs: f.ids, Keyring: keyring, Quota: f.quota, Jobs: f.jobs, Channels: chans, Buttons: buttonKey,
		Inbox: f.inbox, VerifyToken: "verify-me", Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	f.svc.SetReplyHandler(f.replies)
	return f
}

func (f fixture) store(t *testing.T) context.Context {
	t.Helper()
	id := tenant.StoreID{ID: f.ids.New()}
	ctx := tenant.WithStore(t.Context(), id)
	if err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'EGP', 'ar', 'Africa/Cairo')`,
			id.ID, "msg-"+strings.ToLower(id.String()[14:]))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return ctx
}

var customer = func() phone.E164 {
	p, err := phone.Parse("01012345678", phone.EG)
	if err != nil {
		panic(err)
	}
	return p
}()

func (f fixture) enqueue(ctx context.Context, t *testing.T, order *ulid.ID) app.Queued {
	t.Helper()
	var q app.Queued
	if err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) (err error) {
		q, err = f.svc.EnqueueTx(ctx, tx, app.Outgoing{Kind: domain.OrderConfirmation, Locale: domain.Arabic, OrderID: order, To: customer,
			Vars: domain.Vars{CustomerName: "منى", StoreName: "سوق", OrderRef: "7P0R1S2T", Total: "520.00 EGP", ConfirmURL: "https://souq.example.com/ar/o/tok"}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return q
}

// sendJobs are the send jobs enqueued so far.
func (f fixture) sendJobs() []app.SendMessageArgs {
	var out []app.SendMessageArgs
	for _, a := range f.jobs.Args {
		if s, ok := a.(app.SendMessageArgs); ok {
			out = append(out, s)
		}
	}
	return out
}

func (f fixture) message(ctx context.Context, t *testing.T, id ulid.ID) domain.Message {
	t.Helper()
	m, err := f.svc.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (f fixture) used(ctx context.Context, t *testing.T) int64 {
	t.Helper()
	u, err := f.quota.Usage(ctx, entitlements.Messages)
	if err != nil {
		t.Fatal(err)
	}
	return u.Used
}

// deliver passes a channel's webhook through the provider (placing its
// events by store), the inbox and the processing job; it returns the
// outcomes in order.
func (f fixture) deliver(t *testing.T, ch *fake.Channel, events ...channel.Event) []string {
	t.Helper()
	return f.deliverVia(t, f.svc, ch, events...)
}

// deliverVia is deliver through svc.
func (f fixture) deliverVia(t *testing.T, svc *app.Service, ch *fake.Channel, events ...channel.Event) []string {
	t.Helper()
	var provider webhooks.PlatformProvider
	for _, p := range svc.PlatformProviders() {
		if p.Name() == ch.Name() {
			provider = p
		}
	}
	w := ch.Webhook(events...)
	parts, err := provider.VerifyPlatform(t.Context(), webhooks.Request{Header: w.Header, Body: w.Body})
	if err != nil {
		t.Fatal(err)
	}
	var outcomes []string
	for _, p := range parts {
		id := f.inbox.Put(ch.Name(), p.Body)
		if err = svc.ProcessWebhook(tenant.WithStore(t.Context(), p.Store), id, ch.Name()); err != nil {
			t.Fatal(err)
		}
		outcomes = append(outcomes, f.inbox.Outcome(id))
	}
	return outcomes
}

// statusFor is a status event of m in the store of ctx.
func statusFor(ctx context.Context, t *testing.T, m domain.Message, st channel.Status, code string) channel.Event {
	t.Helper()
	store, err := tenant.FromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return channel.Event{Kind: channel.StatusEvent, ID: m.ProviderID + ":" + string(st), ProviderID: m.ProviderID,
		Ref: domain.Ref{Store: store, Message: m.ID}.String(), Status: st, ErrorCode: code}
}

func TestSendByWhatsApp(t *testing.T) {
	t.Parallel()
	f := newFixture(t, options{})
	ctx := f.store(t)
	order := f.ids.New()
	q := f.enqueue(ctx, t, &order)
	if q.Status != domain.Queued || q.Usage.Used != 1 || len(f.sendJobs()) != 1 {
		t.Fatalf("queued = %+v, jobs %v", q, f.jobs.Kinds())
	}
	m := f.message(ctx, t, q.ID)
	if m.ToMasked == "" || strings.Contains(m.ToMasked, "12345678") || strings.Contains(string(m.Content), "1012345678") || *m.OrderID != order {
		t.Fatalf("log entry leaks or misses: %+v", m)
	}
	if err := f.svc.SendMessage(ctx, q.ID, false); err != nil {
		t.Fatal(err)
	}
	sent := f.wa.Sent()
	if len(sent) != 1 || len(f.sms.Sent()) != 0 {
		t.Fatalf("sent wa=%d sms=%d", len(sent), len(f.sms.Sent()))
	}
	s := sent[0]
	if s.Template != "order_confirmation_v1" || s.Language != "ar" || s.To.Unmasked() != "+201012345678" || len(s.Params) != 4 || s.Params[0] != "منى" {
		t.Fatalf("template message = %+v", s)
	}
	for i, want := range []domain.Reply{domain.Confirm, domain.Cancel} {
		ref, reply, ok := buttonKey.ParsePayload(s.Buttons[i].Payload)
		if !ok || reply != want || ref.Message != q.ID {
			t.Fatalf("button %d = %q", i, s.Buttons[i].Payload)
		}
	}
	m = f.message(ctx, t, q.ID)
	if m.Status != domain.Sent || m.Channel != domain.WhatsApp || m.Provider != "fake_whatsapp" || m.ProviderID == "" {
		t.Fatalf("after send = %+v", m)
	}
	// A retried job does not send again.
	if err := f.svc.SendMessage(ctx, q.ID, false); err != nil || len(f.wa.Sent()) != 1 {
		t.Fatalf("retried job sent again: %v", err)
	}

	// Statuses move forward only, in any order, once each.
	got := f.deliver(t, f.wa, statusFor(ctx, t, m, channel.StatusRead, ""), statusFor(ctx, t, m, channel.StatusDelivered, ""),
		statusFor(ctx, t, m, channel.StatusSent, ""))
	if strings.Join(got, ",") != "read,unchanged,unchanged" || f.message(ctx, t, q.ID).Status != domain.Read {
		t.Fatalf("outcomes = %v", got)
	}
	// A failure after delivery does not undo it.
	if got = f.deliver(t, f.wa, statusFor(ctx, t, m, channel.StatusFailed, "131000")); got[0] != "unchanged" {
		t.Fatalf("failed after read = %v", got)
	}
	if f.used(ctx, t) != 1 {
		t.Fatal("quota changed")
	}
}

func TestFallbackWhenNotOnWhatsApp(t *testing.T) {
	t.Parallel()
	f := newFixture(t, options{})
	ctx := f.store(t)
	f.wa.Unreachable(customer.Unmasked())
	q := f.enqueue(ctx, t, nil)
	if err := f.svc.SendMessage(ctx, q.ID, false); err != nil {
		t.Fatal(err)
	}
	m := f.message(ctx, t, q.ID)
	jobs := f.sendJobs()
	if m.Status != domain.Queued || !m.Fallback || m.ErrorCode != "undeliverable" || len(jobs) != 2 || !jobs[1].Fallback {
		t.Fatalf("after WhatsApp refused = %+v, jobs %+v", m, jobs)
	}
	if err := f.svc.SendMessage(ctx, q.ID, false); err != nil {
		t.Fatal(err)
	}
	sms := f.sms.Sent()
	if len(sms) != 1 || !strings.Contains(sms[0].Text, "https://souq.example.com/ar/o/tok") || !strings.Contains(sms[0].Text, "7P0R1S2T") || sms[0].Template != "" {
		t.Fatalf("SMS = %+v", sms)
	}
	if m = f.message(ctx, t, q.ID); m.Status != domain.Sent || m.Channel != domain.SMS || m.Provider != "fake_sms" || f.used(ctx, t) != 1 {
		t.Fatalf("after SMS = %+v (used %d)", m, f.used(ctx, t))
	}
}

// TestFallbackOnFailedStatus: WhatsApp accepts, then reports the customer
// unreachable; the message goes by SMS, and late WhatsApp statuses are
// stale.
func TestFallbackOnFailedStatus(t *testing.T) {
	t.Parallel()
	f := newFixture(t, options{})
	ctx := f.store(t)
	q := f.enqueue(ctx, t, nil)
	if err := f.svc.SendMessage(ctx, q.ID, false); err != nil {
		t.Fatal(err)
	}
	first := f.message(ctx, t, q.ID)
	if got := f.deliver(t, f.wa, statusFor(ctx, t, first, channel.StatusFailed, fake.UndeliverableCode)); got[0] != "fallback" {
		t.Fatalf("outcome = %v", got)
	}
	if err := f.svc.SendMessage(ctx, q.ID, false); err != nil {
		t.Fatal(err)
	}
	m := f.message(ctx, t, q.ID)
	if m.Channel != domain.SMS || m.Status != domain.Sent || len(f.sms.Sent()) != 1 {
		t.Fatalf("after fallback = %+v", m)
	}
	if got := f.deliver(t, f.wa, statusFor(ctx, t, first, channel.StatusDelivered, "")); got[0] != "stale" {
		t.Fatalf("late WhatsApp status = %v", got)
	}
	if got := f.deliver(t, f.sms, statusFor(ctx, t, m, channel.StatusDelivered, "")); got[0] != "delivered" {
		t.Fatalf("SMS status = %v", got)
	}
	// SMS failing too ends the message and returns its unit.
	if got := f.deliver(t, f.sms, statusFor(ctx, t, m, channel.StatusFailed, "x")); got[0] != "unchanged" {
		t.Fatalf("failed after delivered = %v", got)
	}
}

func TestFailuresReturnTheUnit(t *testing.T) {
	t.Parallel()
	f := newFixture(t, options{noSMS: true})
	ctx := f.store(t)

	// Rejected: fails at once.
	f.wa.FailWith(channel.ErrRejected)
	q := f.enqueue(ctx, t, nil)
	if err := f.svc.SendMessage(ctx, q.ID, false); err != nil {
		t.Fatal(err)
	}
	if m := f.message(ctx, t, q.ID); m.Status != domain.Failed || m.ErrorCode != "rejected" || f.used(ctx, t) != 0 {
		t.Fatalf("rejected = %+v (used %d)", m, f.used(ctx, t))
	}

	// Transient: retried, then failed on the last try.
	f.wa.FailWith(errors.New("connection reset"))
	q = f.enqueue(ctx, t, nil)
	if err := f.svc.SendMessage(ctx, q.ID, false); err == nil {
		t.Fatal("transient error swallowed")
	}
	if m := f.message(ctx, t, q.ID); m.Status != domain.Queued || f.used(ctx, t) != 1 {
		t.Fatalf("transient = %+v", m)
	}
	if err := f.svc.SendMessage(ctx, q.ID, true); err != nil {
		t.Fatal(err)
	}
	if m := f.message(ctx, t, q.ID); m.Status != domain.Failed || m.ErrorCode != "unavailable" || f.used(ctx, t) != 0 {
		t.Fatalf("last try = %+v", m)
	}

	// Unreachable on WhatsApp with no SMS: failed.
	f.wa.FailWith(nil)
	f.wa.Unreachable(customer.Unmasked())
	q = f.enqueue(ctx, t, nil)
	if err := f.svc.SendMessage(ctx, q.ID, false); err != nil {
		t.Fatal(err)
	}
	if m := f.message(ctx, t, q.ID); m.Status != domain.Failed || m.ErrorCode != "undeliverable" || f.used(ctx, t) != 0 {
		t.Fatalf("unreachable without SMS = %+v", m)
	}

	// Credentials refused: failed.
	f.wa.FailWith(channel.ErrAuth)
	q = f.enqueue(ctx, t, nil)
	if err := f.svc.SendMessage(ctx, q.ID, false); err != nil {
		t.Fatal(err)
	}
	if m := f.message(ctx, t, q.ID); m.Status != domain.Failed || m.ErrorCode != "auth" {
		t.Fatalf("auth = %+v", m)
	}
}

// TestQuotaExceeded: the acceptance criterion at the messaging side: past
// the quota nothing is sent, the caller's transaction goes on, and the
// usage says so.
func TestQuotaExceeded(t *testing.T) {
	t.Parallel()
	f := newFixture(t, options{limit: 1})
	ctx := f.store(t)
	if q := f.enqueue(ctx, t, nil); q.Status != domain.Queued || q.Usage.CrossedBy(1) != entitlements.Reached {
		t.Fatalf("first = %+v", q)
	}
	q := f.enqueue(ctx, t, nil)
	if q.Status != domain.Skipped || q.Usage.Used != 1 || q.Usage.Remaining() != 0 {
		t.Fatalf("over the quota = %+v", q)
	}
	if len(f.sendJobs()) != 1 {
		t.Fatalf("a skipped message got a send job: %v", f.jobs.Kinds())
	}
	if m := f.message(ctx, t, q.ID); m.Status != domain.Skipped || m.Content != nil || m.QuotaPeriod != nil {
		t.Fatalf("skipped entry = %+v", m)
	}
	if err := f.svc.SendMessage(ctx, q.ID, false); err != nil || len(f.wa.Sent()) != 0 {
		t.Fatalf("a skipped message was sent: %v", err)
	}
}

func TestNoChannel(t *testing.T) {
	t.Parallel()
	f := newFixture(t, options{chans: &app.Channels{}})
	ctx := f.store(t)
	q := f.enqueue(ctx, t, nil)
	if q.Status != domain.Failed || len(f.sendJobs()) != 0 || f.used(ctx, t) != 0 {
		t.Fatalf("no channel = %+v", q)
	}
	if m := f.message(ctx, t, q.ID); m.ErrorCode != "no_channel" {
		t.Fatalf("entry = %+v", m)
	}
	if len(f.svc.PlatformProviders()) != 0 {
		t.Fatal("providers without channels")
	}
}

func TestInvalidOutgoing(t *testing.T) {
	t.Parallel()
	f := newFixture(t, options{})
	ctx := f.store(t)
	for name, o := range map[string]app.Outgoing{
		"kind":   {Kind: "promo", Locale: "ar", To: customer},
		"locale": {Kind: domain.OrderConfirmation, Locale: "fr", To: customer},
		"to":     {Kind: domain.OrderConfirmation, Locale: "ar"},
	} {
		err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
			_, err := f.svc.EnqueueTx(ctx, tx, o)
			return err
		})
		if !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := f.svc.Get(ctx, f.ids.New()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing message: %v", err)
	}
	if err := f.svc.SendMessage(ctx, f.ids.New(), false); err != nil {
		t.Fatalf("send of a missing message: %v", err)
	}
}

func TestButtonReply(t *testing.T) {
	t.Parallel()
	f := newFixture(t, options{})
	ctx := f.store(t)
	order := f.ids.New()
	q := f.enqueue(ctx, t, &order)
	if err := f.svc.SendMessage(ctx, q.ID, false); err != nil {
		t.Fatal(err)
	}
	m := f.message(ctx, t, q.ID)
	confirm, cancel := f.wa.Sent()[0].Buttons[0].Payload, f.wa.Sent()[0].Buttons[1].Payload
	tap := func(id, payload string) channel.Event {
		return channel.Event{Kind: channel.ButtonEvent, ID: id, ProviderID: m.ProviderID, Payload: payload, At: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
	}
	if got := f.deliver(t, f.wa, tap("wamid.IN1", confirm)); got[0] != "reply_confirm" {
		t.Fatalf("confirm = %v", got)
	}
	m = f.message(ctx, t, q.ID)
	if m.Reply != domain.Confirm || m.RepliedAt == nil || m.Status != domain.Read {
		t.Fatalf("after reply = %+v", m)
	}
	// A second tap, even of the other button, changes nothing.
	if got := f.deliver(t, f.wa, tap("wamid.IN2", cancel)); got[0] != "repeated_reply" {
		t.Fatalf("second tap = %v", got)
	}
	if len(f.replies.got) != 1 || f.replies.got[0].Answer != domain.Confirm || *f.replies.got[0].OrderID != order || f.replies.got[0].MessageID != q.ID {
		t.Fatalf("handler saw %+v", f.replies.got)
	}
	// A payload we did not sign is not even placed.
	forged := domain.ButtonKey("another key, another platform....").Payload(domain.Ref{Store: mustStore(ctx, t), Message: q.ID}, domain.Cancel)
	if got := f.deliver(t, f.wa, tap("wamid.IN3", forged)); len(got) != 0 {
		t.Fatalf("forged payload placed: %v", got)
	}
}

func mustStore(ctx context.Context, t *testing.T) tenant.StoreID {
	t.Helper()
	s, err := tenant.FromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestCrossTenant: a store's events never touch another store's
// messages, even with the other's message ID.
func TestCrossTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t, options{})
	a, b := f.store(t), f.store(t)
	qa := f.enqueue(a, t, nil)
	if err := f.svc.SendMessage(a, qa.ID, false); err != nil {
		t.Fatal(err)
	}
	ma := f.message(a, t, qa.ID)
	if _, err := f.svc.Get(b, qa.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("B reads A's message: %v", err)
	}
	// B's reference with A's message ID: placed in B, finds nothing.
	if got := f.deliver(t, f.wa, statusFor(b, t, ma, channel.StatusDelivered, "")); got[0] != "unknown_message" {
		t.Fatalf("A's message under B's reference = %v", got)
	}
	// A button payload for B naming A's message: placed in B, nothing there.
	payload := buttonKey.Payload(domain.Ref{Store: mustStore(b, t), Message: qa.ID}, domain.Cancel)
	if got := f.deliver(t, f.wa, channel.Event{Kind: channel.ButtonEvent, ID: "wamid.X", Payload: payload}); got[0] != "unknown_message" {
		t.Fatalf("A's message under B's payload = %v", got)
	}
	if m := f.message(a, t, qa.ID); m.Status != domain.Sent || m.Reply != domain.NoReply {
		t.Fatalf("A's message changed: %+v", m)
	}
	// A job of store B with a receipt of store A's event.
	id := f.inbox.Put("fake_whatsapp", mustJSON(t, statusFor(a, t, ma, channel.StatusDelivered, "")))
	if err := f.svc.ProcessWebhook(b, id, "fake_whatsapp"); err != nil || f.inbox.Outcome(id) != "unknown_message" {
		t.Fatalf("A's event processed as B: %v %q", err, f.inbox.Outcome(id))
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEarlyStatusWaits(t *testing.T) {
	t.Parallel()
	f := newFixture(t, options{})
	ctx := f.store(t)
	q := f.enqueue(ctx, t, nil)
	m := f.message(ctx, t, q.ID)
	m.ProviderID = "fake_whatsapp.99"
	id := f.inbox.Put("fake_whatsapp", mustJSON(t, statusFor(ctx, t, m, channel.StatusSent, "")))
	if err := f.svc.ProcessWebhook(ctx, id, "fake_whatsapp"); !app.IsEarly(err) {
		t.Fatalf("status before send = %v", err)
	}
	for body, want := range map[string]string{`not json`: "unreadable", `{"kind":"text","id":"x"}`: "ignored"} {
		id = f.inbox.Put("fake_whatsapp", []byte(body))
		if err := f.svc.ProcessWebhook(ctx, id, "fake_whatsapp"); err != nil || f.inbox.Outcome(id) != want {
			t.Errorf("%s: %v %q", body, err, f.inbox.Outcome(id))
		}
	}
	if err := f.svc.ProcessWebhook(ctx, f.ids.New(), "fake_whatsapp"); err != nil {
		t.Fatalf("missing receipt: %v", err)
	}
}

// fetching is an SMS channel that can be asked: what it answers wins
// over the webhook's word.
type fetching struct {
	*fake.Channel
	status channel.Status
	code   string
}

func (f fetching) Fetch(context.Context, string) (channel.Status, string, error) {
	return f.status, f.code, nil
}

func TestFetchedStatusWins(t *testing.T) {
	t.Parallel()
	smsFake := fake.New("fake_sms", []byte("sms-secret"))
	f := newFixture(t, options{chans: &app.Channels{SMS: fetching{Channel: smsFake, status: channel.StatusFailed, code: "30003"}}})
	ctx := f.store(t)
	q := f.enqueue(ctx, t, nil)
	if err := f.svc.SendMessage(ctx, q.ID, false); err != nil {
		t.Fatal(err)
	}
	m := f.message(ctx, t, q.ID)
	if m.Channel != domain.SMS {
		t.Fatalf("SMS only = %+v", m)
	}
	// The webhook claims delivered; the channel says failed.
	if got := f.deliver(t, smsFake, statusFor(ctx, t, m, channel.StatusDelivered, "")); got[0] != "failed" {
		t.Fatalf("outcome = %v", got)
	}
	if m = f.message(ctx, t, q.ID); m.Status != domain.Failed || m.ErrorCode != "30003" || f.used(ctx, t) != 0 {
		t.Fatalf("after fetch = %+v", m)
	}
}

func TestHandshakeAndProviders(t *testing.T) {
	t.Parallel()
	f := newFixture(t, options{})
	providers := f.svc.PlatformProviders()
	if len(providers) != 2 || providers[0].Name() != "fake_whatsapp" || providers[1].Name() != "fake_sms" {
		t.Fatalf("providers = %v", providers)
	}
	q := url.Values{"hub.mode": {"subscribe"}, "hub.verify_token": {"verify-me"}, "hub.challenge": {"42"}}
	if got, err := providers[0].ChallengePlatform(t.Context(), q); err != nil || got != "42" {
		t.Fatalf("handshake = %q, %v", got, err)
	}
	if _, err := providers[1].ChallengePlatform(t.Context(), q); !errors.Is(err, webhooks.ErrForged) {
		t.Fatalf("SMS handshake: %v", err)
	}
	q.Set("hub.verify_token", "guess")
	if _, err := providers[0].ChallengePlatform(t.Context(), q); !errors.Is(err, webhooks.ErrForged) {
		t.Fatalf("wrong token: %v", err)
	}
	// Forged and unreadable deliveries.
	w := f.wa.Webhook(channel.Event{Kind: channel.StatusEvent, ID: "x"})
	w.Body = append(w.Body, ' ')
	if _, err := providers[0].VerifyPlatform(t.Context(), webhooks.Request{Header: w.Header, Body: w.Body}); !errors.Is(err, webhooks.ErrForged) {
		t.Fatalf("forged: %v", err)
	}
	bad := []byte("{")
	parts, err := providers[0].VerifyPlatform(t.Context(), webhooks.Request{Header: map[string][]string{fake.SignatureHeader: {fake.Sign([]byte("wa-secret"), bad)}}, Body: bad})
	if err != nil || len(parts) != 0 {
		t.Fatalf("unreadable: %v %v", parts, err)
	}
	// A customer's text is not placed (6c), and its number never reaches a part.
	text := f.wa.Webhook(channel.Event{Kind: channel.TextEvent, ID: "t", From: "+201012345678", Text: "hi"})
	parts, err = providers[0].VerifyPlatform(t.Context(), webhooks.Request{Header: text.Header, Body: text.Body})
	if err != nil || len(parts) != 0 {
		t.Fatalf("text: %v %v", parts, err)
	}
	// Accepting a part enqueues its processing job, naming the channel.
	ctx := f.store(t)
	rid := f.ids.New()
	if err = postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
		return providers[0].AcceptTx(ctx, tx, webhooks.Receipt{ID: rid})
	}); err != nil {
		t.Fatal(err)
	}
	last, ok := f.jobs.Args[len(f.jobs.Args)-1].(app.ProcessWebhookArgs)
	if !ok || last.ReceiptID != rid || last.Provider != "fake_whatsapp" || last.StoreID != mustStore(ctx, t) {
		t.Fatalf("job = %+v", f.jobs.Args[len(f.jobs.Args)-1])
	}
	if app.PlatformConnection().IsZero() {
		t.Fatal("platform connection is the nil ID")
	}
}

// TestMessageLookupPlan: a webhook's lookup of its message is an index
// search in one partition, for any store, under RLS. The plan:
//
//	Result
//	  One-Time Filter: (current store = $1)
//	  ->  Index Scan using messages_2026_10_pkey on messages_2026_10 messages
//	        Index Cond: ((store_id = …) AND (created_at = …) AND (id = …))
func TestMessageLookupPlan(t *testing.T) {
	t.Parallel()
	f := newFixture(t, options{})
	pool, capture := testkit.CapturingPool(t, infra.AppURL)
	ctx := f.store(t)
	q := f.enqueue(ctx, t, nil)
	if err := postgres.InTenantTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "SELECT 1") // warm the pool
		return err
	}); err != nil {
		t.Fatal(err)
	}
	svc := app.NewService(app.Deps{DB: pool, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if _, err := svc.Get(ctx, q.ID); err != nil {
		t.Fatal(err)
	}
	plan := testkit.GenericPlan(ctx, t, pool, capture.Last(t, "FROM messages"))
	testkit.AssertIndexCond(t, plan, "id = ")
	if n := strings.Count(plan, " on messages_"); n != 1 && !strings.Contains(plan, "Subplans Removed") {
		t.Fatalf("not pruned to one partition (%d scanned):\n%s", n, plan)
	}
	t.Log("\n" + plan)
}

// recorder keeps audit entries.
type recorder struct {
	mu      sync.Mutex
	actions []string
}

func (r *recorder) RecordTx(_ context.Context, _ pgx.Tx, e audit.Entry) error {
	if err := e.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.actions = append(r.actions, e.Action+" "+e.TargetID)
	return nil
}

func TestTexts(t *testing.T) {
	t.Parallel()
	f := newFixture(t, options{noWA: true})
	rec := &recorder{}
	keyring, err := secretbox.ParseKeyring("1:MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatal(err)
	}
	svc := app.NewService(app.Deps{DB: f.pool, IDs: f.ids, Keyring: keyring, Quota: f.quota, Jobs: f.jobs, Channels: app.Channels{SMS: f.sms},
		Audit: rec, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	ctx := f.store(t)
	texts, err := svc.Texts(ctx)
	if err != nil || len(texts) != 2 || texts[0].Custom || texts[0].Locale != "ar" || texts[1].Locale != "en" {
		t.Fatalf("defaults = %+v, %v", texts, err)
	}
	own := "{{store}}: {{name}}، أكّد {{ref}} هنا {{url}}"
	saved, err := svc.SaveText(ctx, domain.OrderConfirmation, "ar", own)
	if err != nil || !saved.Custom || saved.UpdatedAt == nil {
		t.Fatalf("save = %+v, %v", saved, err)
	}
	if texts, err = svc.Texts(ctx); err != nil || !texts[0].Custom || texts[0].SMSBody != own || texts[1].Custom {
		t.Fatalf("after save = %+v, %v", texts, err)
	}
	// The send job writes the store's text.
	q := f.enqueue(ctx, t, nil)
	if err = svc.SendMessage(ctx, q.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := f.sms.Sent(); len(got) != 1 || got[0].Text != "سوق: منى، أكّد 7P0R1S2T هنا https://souq.example.com/ar/o/tok" {
		t.Fatalf("SMS = %+v", got)
	}
	for name, err := range map[string]error{
		"bad body":   func() error { _, e := svc.SaveText(ctx, domain.OrderConfirmation, "ar", "no link"); return e }(),
		"bad kind":   func() error { _, e := svc.SaveText(ctx, "promo", "ar", "{{url}}"); return e }(),
		"bad locale": func() error { _, e := svc.ResetText(ctx, domain.OrderConfirmation, "fr"); return e }(),
	} {
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	reset, err := svc.ResetText(ctx, domain.OrderConfirmation, "ar")
	if err != nil || reset.Custom || reset.SMSBody != domain.DefaultSMS(domain.OrderConfirmation, "ar") {
		t.Fatalf("reset = %+v, %v", reset, err)
	}
	if _, err = svc.ResetText(ctx, domain.OrderConfirmation, "ar"); err != nil {
		t.Fatalf("second reset: %v", err)
	}
	if strings.Join(rec.actions, "|") != "message_text.updated order_confirmation:ar|message_text.reset order_confirmation:ar" {
		t.Fatalf("audit = %v", rec.actions)
	}
}

func TestLogPagesAndUsage(t *testing.T) {
	t.Parallel()
	f := newFixture(t, options{limit: 10})
	ctx := f.store(t)
	order := f.ids.New()
	first := f.enqueue(ctx, t, &order)
	for range 4 {
		f.enqueue(ctx, t, nil)
	}
	if err := f.svc.SendMessage(ctx, first.ID, false); err != nil {
		t.Fatal(err)
	}
	var seen []ulid.ID
	var after *domain.Message
	for {
		p, err := f.svc.Messages(ctx, after, "", 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range p.Items {
			seen = append(seen, m.ID)
		}
		if p.Next == nil {
			break
		}
		after = p.Next
	}
	if len(seen) != 5 || seen[4] != first.ID {
		t.Fatalf("paged %v", seen)
	}
	if p, err := f.svc.Messages(ctx, nil, domain.Sent, 0); err != nil || len(p.Items) != 1 || p.Items[0].ID != first.ID {
		t.Fatalf("sent only = %+v, %v", p, err)
	}
	if list, err := f.svc.OrderMessages(ctx, order); err != nil || len(list) != 1 || list[0].ID != first.ID {
		t.Fatalf("order messages = %+v, %v", list, err)
	}
	for _, bad := range []struct {
		status domain.Status
		limit  int
	}{{"lost", 10}, {"", 201}, {"", -1}} {
		if _, err := f.svc.Messages(ctx, nil, bad.status, bad.limit); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	if u, err := f.svc.Usage(ctx); err != nil || u.Used != 5 || u.Limit != 10 {
		t.Fatalf("usage = %+v, %v", u, err)
	}
}

// TestLogListPlan: a page of the log reads each month's partition by its
// primary key, newest first, under RLS; no sort, no scan of the table.
func TestLogListPlan(t *testing.T) {
	t.Parallel()
	f := newFixture(t, options{})
	pool, capture := testkit.CapturingPool(t, infra.AppURL)
	ctx := f.store(t)
	f.enqueue(ctx, t, nil)
	svc := app.NewService(app.Deps{DB: pool, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if _, err := svc.Messages(ctx, nil, "", 50); err != nil {
		t.Fatal(err)
	}
	plan := testkit.GenericPlan(ctx, t, pool, capture.Last(t, "FROM messages"))
	testkit.AssertIndexCond(t, plan, "store_id = ")
	if strings.Contains(plan, "Seq Scan") || strings.Contains(plan, "Sort") {
		t.Fatalf("log page sorts or scans:\n%s", plan)
	}
	t.Log("\n" + plan)
}

func pgxTx(ctx context.Context, db *pgxpool.Pool, fn func(pgx.Tx) error) error {
	return postgres.InTenantTx(ctx, db, fn)
}
