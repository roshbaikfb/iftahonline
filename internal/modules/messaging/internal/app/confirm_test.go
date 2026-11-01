package app_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/channel"
	"github.com/roshbaikfb/iftahonline/internal/modules/messaging/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/platform/mailer"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/secretbox"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// fakeOrders is orders.Fulfilment in memory: a move from anything but
// pending is refused, as the state machine does for confirm and cancel.
type fakeOrders struct {
	mu     sync.Mutex
	orders map[ulid.ID]orders.Order
	moves  []string
}

func (f *fakeOrders) add(t *testing.T, ids *ulid.Generator, method orders.PaymentMethod) ulid.ID {
	t.Helper()
	total, err := money.New(52000, money.EGP)
	if err != nil {
		t.Fatal(err)
	}
	o := orders.Order{ID: ids.New(), Status: orders.StatusPending, PaymentMethod: method, Total: total, CreatedAt: time.Now().UTC()}
	o.Contact.Name, o.Contact.Phone = "منى", customer
	f.mu.Lock()
	defer f.mu.Unlock()
	f.orders[o.ID] = o
	return o.ID
}

func (f *fakeOrders) setRisk(id ulid.ID, a orders.RiskAction) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o := f.orders[id]
	o.RiskAction = a
	f.orders[id] = o
}

// setDeposit makes id a prepaid delivery awaiting its 65 EGP (ADR-0067).
func (f *fakeOrders) setDeposit(t *testing.T, id ulid.ID) {
	t.Helper()
	deposit, err := money.New(6500, money.EGP)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	o := f.orders[id]
	o.RiskAction, o.Deposit, o.DepositStatus = orders.RiskPrepay, deposit, orders.PaymentAwaiting
	f.orders[id] = o
}

func (f *fakeOrders) set(id ulid.ID, st orders.Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o := f.orders[id]
	o.Status = st
	f.orders[id] = o
}

func (f *fakeOrders) Get(_ context.Context, id ulid.ID) (orders.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.orders[id]
	if !ok {
		return orders.Order{}, orders.ErrOrderNotFound
	}
	return o, nil
}

func (f *fakeOrders) GetTx(ctx context.Context, _ pgx.Tx, id ulid.ID) (orders.Order, error) {
	return f.Get(ctx, id)
}

func (f *fakeOrders) TransitionSystemTx(ctx context.Context, tx pgx.Tx, id ulid.ID, to orders.Status, actor, note string) (bool, error) {
	return f.TransitionSystemReasonTx(ctx, tx, id, to, actor, note, orders.ReasonNone)
}

func (f *fakeOrders) TransitionSystemReasonTx(_ context.Context, _ pgx.Tx, id ulid.ID, to orders.Status, actor, _ string,
	reason orders.OutcomeReason) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o := f.orders[id]
	if o.Status != orders.StatusPending {
		return false, fmt.Errorf("%w: %s to %s", orders.ErrInvalidTransition, o.Status, to)
	}
	if to == orders.StatusConfirmed { // as orders refuses them (ADR-0066, ADR-0070)
		byCustomer := strings.HasPrefix(actor, "customer:")
		switch {
		case o.RiskAction == orders.RiskHold:
			return false, orders.ErrOrderHeld
		case o.RiskAction == orders.RiskVerify && byCustomer:
			return false, orders.ErrNeedsCall
		case o.DepositStatus == orders.PaymentAwaiting && byCustomer:
			return false, orders.ErrDepositDue
		}
	}
	o.Status, o.OutcomeReason = to, reason
	f.orders[id] = o
	f.moves = append(f.moves, actor+">"+string(to))
	return true, nil
}

func (f *fakeOrders) reason(id ulid.ID) orders.OutcomeReason {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.orders[id].OutcomeReason
}

func (f *fakeOrders) status(id ulid.ID) orders.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.orders[id].Status
}

type sink struct {
	mu  sync.Mutex
	got []mailer.Message
}

func (s *sink) Send(_ context.Context, m mailer.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, m)
	return nil
}

type cfix struct {
	fixture
	svc    *app.Service
	orders *fakeOrders
	mail   *sink
}

func newConfirmFixture(t *testing.T, o options) cfix {
	t.Helper()
	f := newFixture(t, o)
	keyring, err := secretbox.ParseKeyring("1:MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatal(err)
	}
	limit := o.limit
	if limit == 0 {
		limit = 100
	}
	ent, err := entitlements.New(entitlements.Deps{Pool: f.pool, Source: entitlements.Fixed{entitlements.Messages: {Value: limit}}})
	if err != nil {
		t.Fatal(err)
	}
	c := cfix{fixture: f, orders: &fakeOrders{orders: map[ulid.ID]orders.Order{}}, mail: &sink{}}
	chans := app.Channels{WhatsApp: f.wa, SMS: f.sms}
	if o.chans != nil {
		chans = *o.chans
	}
	c.svc = app.NewService(app.Deps{DB: f.pool, IDs: f.ids, Keyring: keyring, Quota: ent.Entitlements(), Jobs: f.jobs, Channels: chans,
		Buttons: buttonKey, Inbox: f.inbox, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Confirmations: app.Confirmations{Number: o.number, Orders: c.orders, Mailer: c.mail, DashboardURL: "https://app.example.com",
			Stores: func(context.Context) (app.StoreInfo, error) {
				return app.StoreInfo{Name: "سوق", Locale: "en", Host: "souq.example.com"}, nil
			},
			Contacts: func(context.Context) ([]string, error) {
				return []string{"owner@example.com", "manager@example.com"}, nil
			}}})
	c.quota = ent.Entitlements()
	return c
}

func (c cfix) jobsOf(kind string) []any {
	var out []any
	for _, a := range c.jobs.Args {
		if a.Kind() == kind {
			out = append(out, a)
		}
	}
	return out
}

func (c cfix) confirmation(ctx context.Context, t *testing.T, order ulid.ID) domain.Confirmation {
	t.Helper()
	got, err := c.svc.Confirmation(ctx, order)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (c cfix) noticeKinds(ctx context.Context, t *testing.T) string {
	t.Helper()
	p, err := c.svc.Notifications(ctx, nil, false, 50)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, n := range p.Items {
		kinds = append(kinds, string(n.Kind))
	}
	return strings.Join(kinds, ",")
}

func TestConfirmOrder(t *testing.T) {
	t.Parallel()
	c := newConfirmFixture(t, options{})
	ctx := c.store(t)
	id := c.orders.add(t, c.ids, orders.PaymentCOD)
	for range 2 { // a redelivered event changes nothing
		if err := c.svc.ConfirmOrder(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	got := c.confirmation(ctx, t, id)
	if got.Status != domain.Pending || got.MessageID == nil || time.Until(got.ExpiresAt) < 23*time.Hour {
		t.Fatalf("confirmation = %+v", got)
	}
	if n := len(c.sendJobs()); n != 1 {
		t.Fatalf("send jobs = %d", n)
	}
	deadlines := c.jobsOf("messaging.auto_cancel_order")
	if len(deadlines) != 1 || deadlines[0].(app.AutoCancelArgs).OrderID != id {
		t.Fatalf("deadline jobs = %+v", deadlines)
	}
	if err := c.svc.SendMessage(ctx, *got.MessageID, false); err != nil {
		t.Fatal(err)
	}
	m := c.wa.Sent()[0]
	if m.Language != "en" || m.Params[1] != "سوق" || m.Params[2] != orders.CodeOf(id) || m.Params[3] != "EGP 520.00" {
		t.Fatalf("message = %+v", m)
	}

	// Card orders, orders no longer pending, unknown orders: nothing.
	card := c.orders.add(t, c.ids, orders.PaymentCard)
	moved := c.orders.add(t, c.ids, orders.PaymentCOD)
	c.orders.set(moved, orders.StatusConfirmed)
	for _, other := range []ulid.ID{card, moved, c.ids.New()} {
		if err := c.svc.ConfirmOrder(ctx, other); err != nil {
			t.Fatal(err)
		}
		if _, err := c.svc.Confirmation(ctx, other); !errors.Is(err, app.ErrConfirmationNotFound) {
			t.Fatalf("confirmation of %s: %v", other, err)
		}
	}

	// A store that turned confirmation off.
	off := c.store(t)
	if _, err := c.svc.SaveSettings(off, domain.Settings{ConfirmCOD: false, AutoCancelHours: 3}); err != nil {
		t.Fatal(err)
	}
	quiet := c.orders.add(t, c.ids, orders.PaymentCOD)
	if err := c.svc.ConfirmOrder(off, quiet); err != nil {
		t.Fatal(err)
	}
	if _, err := c.svc.Confirmation(off, quiet); !errors.Is(err, app.ErrConfirmationNotFound) {
		t.Fatalf("confirmation with the setting off: %v", err)
	}
}

func TestConfirmationAnswers(t *testing.T) {
	t.Parallel()
	c := newConfirmFixture(t, options{})
	ctx := c.store(t)
	answer := func(id ulid.ID, r domain.Reply) domain.ConfirmationStatus {
		t.Helper()
		var st domain.ConfirmationStatus
		if err := pgxTx(ctx, c.pool, func(tx pgx.Tx) (err error) {
			st, _, err = c.svc.AnswerTx(ctx, tx, id, r, domain.ActorWhatsApp, time.Now())
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return st
	}
	yes, no, late := c.orders.add(t, c.ids, orders.PaymentCOD), c.orders.add(t, c.ids, orders.PaymentCOD), c.orders.add(t, c.ids, orders.PaymentCOD)
	for _, id := range []ulid.ID{yes, no, late} {
		if err := c.svc.ConfirmOrder(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if st := answer(yes, domain.Confirm); st != domain.Confirmed || c.orders.status(yes) != orders.StatusConfirmed {
		t.Fatalf("confirm = %s, order %s", st, c.orders.status(yes))
	}
	if st := answer(yes, domain.Cancel); st != domain.Confirmed || c.orders.status(yes) != orders.StatusConfirmed {
		t.Fatalf("second answer = %s, order %s", st, c.orders.status(yes))
	}
	if st := answer(no, domain.Cancel); st != domain.Cancelled || c.orders.status(no) != orders.StatusCancelled {
		t.Fatalf("cancel = %s", st)
	}
	c.orders.set(late, orders.StatusConfirmed) // the merchant first
	if st := answer(late, domain.Cancel); st != domain.Stale || c.orders.status(late) != orders.StatusConfirmed {
		t.Fatalf("late = %s", st)
	}
	got := c.confirmation(ctx, t, yes)
	if got.AnsweredBy != domain.ActorWhatsApp || got.AnsweredAt == nil {
		t.Fatalf("answered = %+v", got)
	}
	if strings.Join(c.orders.moves, ",") != "customer:whatsapp>confirmed,customer:whatsapp>cancelled" {
		t.Fatalf("moves = %v", c.orders.moves)
	}
	if kinds := c.noticeKinds(ctx, t); kinds != "order_cancelled,order_confirmed" {
		t.Fatalf("notices = %s", kinds)
	}
	if err := pgxTx(ctx, c.pool, func(tx pgx.Tx) error {
		_, _, err := c.svc.AnswerTx(ctx, tx, c.ids.New(), domain.Confirm, domain.ActorWhatsApp, time.Now())
		return err
	}); !errors.Is(err, app.ErrConfirmationNotFound) {
		t.Fatalf("no confirmation: %v", err)
	}
	// The reply path: a tap on the message's button.
	tapped := c.orders.add(t, c.ids, orders.PaymentCOD)
	if err := c.svc.ConfirmOrder(ctx, tapped); err != nil {
		t.Fatal(err)
	}
	mid := *c.confirmation(ctx, t, tapped).MessageID
	if err := c.svc.SendMessage(ctx, mid, false); err != nil {
		t.Fatal(err)
	}
	msgs := c.wa.Sent()
	payload := msgs[len(msgs)-1].Buttons[0].Payload
	if got := c.deliverVia(t, c.svc, c.wa, channel.Event{Kind: channel.ButtonEvent, ID: "wamid.tap", Payload: payload}); got[0] != "reply_confirm" {
		t.Fatalf("tap = %v", got)
	}
	if c.orders.status(tapped) != orders.StatusConfirmed {
		t.Fatalf("tapped order = %s", c.orders.status(tapped))
	}
}

func TestAutoCancelJob(t *testing.T) {
	t.Parallel()
	c := newConfirmFixture(t, options{})
	ctx := c.store(t)
	reachedID, lostID, movedID := c.orders.add(t, c.ids, orders.PaymentCOD), c.orders.add(t, c.ids, orders.PaymentCOD), c.orders.add(t, c.ids, orders.PaymentCOD)
	for _, id := range []ulid.ID{reachedID, lostID, movedID} {
		if err := c.svc.ConfirmOrder(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []ulid.ID{reachedID, movedID} {
		if err := c.svc.SendMessage(ctx, *c.confirmation(ctx, t, id).MessageID, false); err != nil {
			t.Fatal(err)
		}
	}
	c.orders.set(movedID, orders.StatusConfirmed)
	for _, id := range []ulid.ID{reachedID, lostID, movedID, c.ids.New()} {
		for range 2 { // a retried job changes nothing more
			if err := c.svc.AutoCancel(ctx, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got := c.confirmation(ctx, t, reachedID); got.Status != domain.Expired || got.AnsweredBy != domain.ActorAutoCancel || c.orders.status(reachedID) != orders.StatusCancelled ||
		c.orders.reason(reachedID) != orders.ReasonUnconfirmed {
		t.Fatalf("reached = %+v, order %s", got, c.orders.status(reachedID))
	}
	if got := c.confirmation(ctx, t, lostID); got.Status != domain.Unreached || c.orders.status(lostID) != orders.StatusPending {
		t.Fatalf("lost = %+v", got)
	}
	if got := c.confirmation(ctx, t, movedID); got.Status != domain.Stale || c.orders.status(movedID) != orders.StatusConfirmed {
		t.Fatalf("moved = %+v", got)
	}
	if kinds := c.noticeKinds(ctx, t); kinds != "confirmation_unreached,order_auto_cancelled" {
		t.Fatalf("notices = %s", kinds)
	}
}

func TestQuotaWarnings(t *testing.T) {
	t.Parallel()
	c := newConfirmFixture(t, options{limit: 5})
	ctx := c.store(t)
	var last ulid.ID
	for range 7 {
		last = c.orders.add(t, c.ids, orders.PaymentCOD)
		if err := c.svc.ConfirmOrder(ctx, last); err != nil {
			t.Fatal(err)
		}
	}
	if got := c.confirmation(ctx, t, last); got.Status != domain.QuotaSkipped {
		t.Fatalf("over the quota = %+v", got)
	}
	if kinds := c.noticeKinds(ctx, t); kinds != "quota_reached,quota_nearly" {
		t.Fatalf("notices = %s", kinds)
	}
	emails := c.jobsOf("messaging.email_notice")
	if len(emails) != 2 {
		t.Fatalf("email jobs = %d", len(emails))
	}
	if n := len(c.jobsOf("messaging.auto_cancel_order")); n != 5 {
		t.Fatalf("deadline jobs = %d, want 5 (none for skipped orders)", n)
	}
	for _, e := range emails {
		if err := c.svc.EmailNotice(ctx, e.(app.EmailNoticeArgs).NotificationID); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.mail.got) != 4 || c.mail.got[0].To != "owner@example.com" || !strings.Contains(c.mail.got[0].Subject, "80%") ||
		!strings.Contains(c.mail.got[2].Subject, "used up") {
		t.Fatalf("emails = %+v", c.mail.got)
	}
	if err := c.svc.EmailNotice(ctx, c.ids.New()); err != nil {
		t.Fatalf("missing notice: %v", err)
	}
}

func TestConfirmWithoutChannelsOrWorker(t *testing.T) {
	t.Parallel()
	c := newConfirmFixture(t, options{chans: &app.Channels{}})
	ctx := c.store(t)
	id := c.orders.add(t, c.ids, orders.PaymentCOD)
	if err := c.svc.ConfirmOrder(ctx, id); err != nil {
		t.Fatal(err)
	}
	if got := c.confirmation(ctx, t, id); got.Status != domain.Unreached {
		t.Fatalf("no channel = %+v", got)
	}
	// The api role has no orders: the worker's jobs refuse to run there.
	api := app.NewService(app.Deps{DB: c.pool, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := api.ConfirmOrder(ctx, id); err == nil {
		t.Fatal("ConfirmOrder without orders")
	}
	if err := api.AutoCancel(ctx, id); err == nil {
		t.Fatal("AutoCancel without orders")
	}
	if err := api.EmailNotice(ctx, c.ids.New()); err != nil {
		t.Fatalf("EmailNotice without a mailer: %v", err)
	}
}

// tokenOf reads the SMS link's token from a sent SMS.
func tokenOf(t *testing.T, text string) string {
	t.Helper()
	_, after, ok := strings.Cut(text, "/o/")
	if !ok || len(after) < 22 {
		t.Fatalf("no link in %q", text)
	}
	return after[:22]
}

// TestSMSLink: a customer not on WhatsApp gets the SMS with the link;
// the link's page shows the order and takes the answer (customer:sms),
// once; another store's host finds nothing behind the token.
func TestSMSLink(t *testing.T) {
	t.Parallel()
	c := newConfirmFixture(t, options{})
	ctx, other := c.store(t), c.store(t)
	c.wa.Unreachable(customer.Unmasked())
	id := c.orders.add(t, c.ids, orders.PaymentCOD)
	if err := c.svc.ConfirmOrder(ctx, id); err != nil {
		t.Fatal(err)
	}
	mid := *c.confirmation(ctx, t, id).MessageID
	for range 2 { // WhatsApp refuses, then the SMS goes
		if err := c.svc.SendMessage(ctx, mid, false); err != nil {
			t.Fatal(err)
		}
	}
	sms := c.sms.Sent()
	if len(sms) != 1 || !strings.Contains(sms[0].Text, "https://souq.example.com/en/o/") {
		t.Fatalf("SMS = %+v", sms)
	}
	token := tokenOf(t, sms[0].Text)

	v, err := c.svc.LinkConfirmation(ctx, token)
	if err != nil || v.OrderCode != orders.CodeOf(id) || v.FirstName != "منى" || v.Status != domain.Pending || v.Total.Amount() != 52000 {
		t.Fatalf("view = %+v, %v", v, err)
	}
	for _, bad := range []struct {
		ctx   context.Context
		token string
	}{{other, token}, {ctx, "nope"}, {ctx, ""}, {ctx, strings.Repeat("a", 65)}} {
		if _, err = c.svc.LinkConfirmation(bad.ctx, bad.token); !errors.Is(err, app.ErrConfirmationNotFound) {
			t.Errorf("%q: %v", bad.token, err)
		}
		if _, err = c.svc.AnswerLink(bad.ctx, bad.token, domain.Cancel); !errors.Is(err, app.ErrConfirmationNotFound) {
			t.Errorf("answer %q: %v", bad.token, err)
		}
	}
	if c.orders.status(id) != orders.StatusPending {
		t.Fatal("a wrong token moved the order")
	}
	if v, err = c.svc.AnswerLink(ctx, token, domain.Confirm); err != nil || v.Status != domain.Confirmed {
		t.Fatalf("answer = %+v, %v", v, err)
	}
	if v, err = c.svc.AnswerLink(ctx, token, domain.Cancel); err != nil || v.Status != domain.Confirmed {
		t.Fatalf("second answer = %+v, %v", v, err)
	}
	if strings.Join(c.orders.moves, ",") != "customer:sms>confirmed" || c.confirmation(ctx, t, id).AnsweredBy != domain.ActorSMSLink {
		t.Fatalf("moves = %v", c.orders.moves)
	}
	// The storefront role's side: orders only, no channels or secrets.
	links := app.NewService(app.Deps{DB: c.pool, IDs: c.ids, Jobs: c.jobs, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Confirmations: app.Confirmations{Orders: c.orders}})
	if v, err = links.LinkConfirmation(ctx, token); err != nil || v.Status != domain.Confirmed {
		t.Fatalf("links view = %+v, %v", v, err)
	}
}

// TestUnreachedMayStillAnswer: a customer whose message was reported
// unreached, but who answers after all, settles the order.
func TestUnreachedMayStillAnswer(t *testing.T) {
	t.Parallel()
	c := newConfirmFixture(t, options{})
	ctx := c.store(t)
	id := c.orders.add(t, c.ids, orders.PaymentCOD)
	if err := c.svc.ConfirmOrder(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := c.svc.AutoCancel(ctx, id); err != nil { // the message never went: unreached
		t.Fatal(err)
	}
	var st domain.ConfirmationStatus
	if err := pgxTx(ctx, c.pool, func(tx pgx.Tx) (err error) {
		st, _, err = c.svc.AnswerTx(ctx, tx, id, domain.Confirm, domain.ActorSMSLink, time.Now())
		return err
	}); err != nil || st != domain.Confirmed || c.orders.status(id) != orders.StatusConfirmed {
		t.Fatalf("late answer = %s, %v", st, err)
	}
}

// customerFirst opens an order's confirmation in a store whose platform
// has a WhatsApp number to confirm on.
func customerFirst(t *testing.T) (cfix, context.Context, ulid.ID, domain.Confirmation) {
	t.Helper()
	c := newConfirmFixture(t, options{number: "+201000000000"})
	ctx := c.store(t)
	id := c.orders.add(t, c.ids, orders.PaymentCOD)
	if err := c.svc.ConfirmOrder(ctx, id); err != nil {
		t.Fatal(err)
	}
	return c, ctx, id, c.confirmation(ctx, t, id)
}

func TestCustomerFirstCode(t *testing.T) {
	t.Parallel()
	c, ctx, id, conf := customerFirst(t)
	if len(conf.WACode) != domain.CodeLen {
		t.Fatalf("code = %q", conf.WACode)
	}
	// The paid template waits for the free path.
	var at time.Time
	for i, a := range c.jobs.Args {
		if a.Kind() == "messaging.send_message" {
			at = c.jobs.Opts[i].ScheduledAt
		}
	}
	if d := time.Until(at); d < app.CustomerFirst-time.Minute || d > app.CustomerFirst {
		t.Fatalf("template scheduled in %v", d)
	}
	link, err := c.svc.WhatsAppLink(ctx, id, "ar")
	if err != nil || !strings.HasPrefix(link, "https://wa.me/201000000000?text=") || !strings.Contains(link, conf.WACode) {
		t.Fatalf("link = %q, %v", link, err)
	}
	if link, err = c.svc.WhatsAppLink(ctx, c.ids.New(), "ar"); err != nil || link != "" {
		t.Fatalf("no confirmation: %q %v", link, err)
	}
	// Without a number, no code and no wait.
	plain := newConfirmFixture(t, options{})
	pctx := plain.store(t)
	pid := plain.orders.add(t, plain.ids, orders.PaymentCOD)
	if err = plain.svc.ConfirmOrder(pctx, pid); err != nil {
		t.Fatal(err)
	}
	if plain.confirmation(pctx, t, pid).WACode != "" {
		t.Fatal("a code without a number")
	}
	if link, err = plain.svc.WhatsAppLink(pctx, pid, "ar"); err != nil || link != "" {
		t.Fatalf("link without a number: %q %v", link, err)
	}
}

// TestCustomerConfirmsByText: the customer sends the ready text from the
// order's phone: the order is confirmed, they are thanked (no quota), and
// the paid template, when its time comes, is not sent and its unit
// comes back.
func TestCustomerConfirmsByText(t *testing.T) {
	t.Parallel()
	c, ctx, id, conf := customerFirst(t)
	text := func(from, body string) []string {
		t.Helper()
		return c.deliverVia(t, c.svc, c.wa, channel.Event{Kind: channel.TextEvent, ID: "wamid.txt." + from + body, From: from, Text: body})
	}
	// Someone else's phone with the right code: placed, refused.
	if got := text("+201099999999", domain.CustomerConfirmText("ar", conf.WACode)); len(got) != 1 || got[0] != "phone_mismatch" {
		t.Fatalf("other phone = %v", got)
	}
	// No code, or a code no order has: not placed at all.
	if got := text(customer.Unmasked(), "مرحبا"); len(got) != 0 {
		t.Fatalf("no code = %v", got)
	}
	if got := text(customer.Unmasked(), "أؤكد طلبي رقم ZZZZZZZZ"); len(got) != 0 {
		t.Fatalf("unknown code = %v", got)
	}
	if c.orders.status(id) != orders.StatusPending {
		t.Fatal("moved by the wrong sender")
	}
	usedBefore := c.used(ctx, t)
	if got := text(customer.Unmasked(), "أؤكد طلبي رقم "+strings.ToLower(conf.WACode)); len(got) != 1 || got[0] != "text_confirmed" {
		t.Fatalf("customer = %v", got)
	}
	if c.orders.status(id) != orders.StatusConfirmed || strings.Join(c.orders.moves, ",") != "customer:whatsapp>confirmed" {
		t.Fatalf("order = %s %v", c.orders.status(id), c.orders.moves)
	}
	if got := text(customer.Unmasked(), "أؤكد طلبي رقم "+conf.WACode+" مرة أخرى"); got[0] != "repeated_text" {
		t.Fatalf("again = %v", got)
	}
	// The thank-you text: free, WhatsApp, in the store's locale.
	if c.used(ctx, t) != usedBefore {
		t.Fatal("the thanks cost a quota unit")
	}
	jobs := c.sendJobs()
	ack := jobs[len(jobs)-1]
	if err := c.svc.SendMessage(ctx, ack.MessageID, false); err != nil {
		t.Fatal(err)
	}
	sent := c.wa.Sent()
	if last := sent[len(sent)-1]; last.Template != "" || !strings.Contains(last.Text, "is confirmed") || !strings.Contains(last.Text, orders.CodeOf(id)) {
		t.Fatalf("thanks = %+v", last)
	}
	// The template's time comes: the customer answered first.
	if err := c.svc.SendMessage(ctx, *conf.MessageID, false); err != nil {
		t.Fatal(err)
	}
	m := c.message(ctx, t, *conf.MessageID)
	if m.Status != domain.Failed || m.ErrorCode != "answered_first" || c.used(ctx, t) != usedBefore-1 {
		t.Fatalf("template = %+v, used %d", m, c.used(ctx, t))
	}
	if n := len(c.wa.Sent()); n != len(sent) {
		t.Fatal("the template went out after the answer")
	}
	if link, err := c.svc.WhatsAppLink(ctx, id, "ar"); err != nil || link != "" {
		t.Fatalf("link after the answer: %q %v", link, err)
	}
}

// TestButtonAnswerThanks: a tap on the template's button is thanked too,
// and a thank-you never falls back to SMS.
func TestButtonAnswerThanks(t *testing.T) {
	t.Parallel()
	c := newConfirmFixture(t, options{})
	ctx := c.store(t)
	id := c.orders.add(t, c.ids, orders.PaymentCOD)
	if err := c.svc.ConfirmOrder(ctx, id); err != nil {
		t.Fatal(err)
	}
	mid := *c.confirmation(ctx, t, id).MessageID
	if err := c.svc.SendMessage(ctx, mid, false); err != nil {
		t.Fatal(err)
	}
	payload := c.wa.Sent()[0].Buttons[1].Payload
	if got := c.deliverVia(t, c.svc, c.wa, channel.Event{Kind: channel.ButtonEvent, ID: "wamid.tap.cancel", Payload: payload}); got[0] != "reply_cancel" {
		t.Fatalf("tap = %v", got)
	}
	jobs := c.sendJobs()
	ack := jobs[len(jobs)-1].MessageID
	c.wa.Unreachable(customer.Unmasked())
	if err := c.svc.SendMessage(ctx, ack, false); err != nil {
		t.Fatal(err)
	}
	if m := c.message(ctx, t, ack); m.Kind != domain.AnswerAck || m.Status != domain.Failed || m.ErrorCode != "undeliverable" {
		t.Fatalf("thanks = %+v", m)
	}
	if len(c.sms.Sent()) != 0 {
		t.Fatal("a thank-you went by SMS")
	}
}

// TestConfirmRiskRules (ADR-0066, ADR-0070): where the store confirms,
// every order gets a confirmation, so the thank-you page is the same for
// all: a held one is sent nothing and never cancelled, a prepaid one is
// sent the link to pay. The extra confirmation asks even where the store
// does not confirm, and the customer's yes leaves it pending with a call
// to make; nor is the thank-you page's link offered there.
func TestConfirmRiskRules(t *testing.T) {
	t.Parallel()
	c := newConfirmFixture(t, options{number: "+201000000000"})
	ctx := c.store(t)
	plain := c.orders.add(t, c.ids, orders.PaymentCOD)
	held := c.orders.add(t, c.ids, orders.PaymentCOD)
	c.orders.setRisk(held, orders.RiskHold)
	prepaid := c.orders.add(t, c.ids, orders.PaymentCOD)
	c.orders.setDeposit(t, prepaid)
	for _, id := range []ulid.ID{plain, held, prepaid} {
		if err := c.svc.ConfirmOrder(ctx, id); err != nil {
			t.Fatal(err)
		}
		if link, err := c.svc.WhatsAppLink(ctx, id, "ar"); err != nil || link == "" {
			t.Fatalf("%s: the thank-you link = %q %v", id, link, err)
		}
	}
	if got := c.confirmation(ctx, t, held); got.Status != domain.Pending || got.MessageID != nil {
		t.Fatalf("a held order's confirmation: %+v", got)
	}
	if n := len(c.jobsOf("messaging.auto_cancel_order")); n != 1 {
		t.Fatalf("%d deadlines, want the plain order's only", n)
	}
	pc := c.confirmation(ctx, t, prepaid)
	if pc.MessageID == nil || c.message(ctx, t, *pc.MessageID).Kind != domain.DeliveryPrepay {
		t.Fatalf("a prepaid order's message: %+v", pc)
	}
	if err := c.svc.SendMessage(ctx, *pc.MessageID, false); err != nil {
		t.Fatal(err)
	}
	sent := c.wa.Sent()
	last := sent[len(sent)-1]
	if last.Template != "order_delivery_prepay_v1" || len(last.Params) != 5 || last.Params[3] != "EGP 65.00" ||
		!strings.HasPrefix(last.Params[4], "https://souq.example.com/en/o/") {
		t.Fatalf("the prepaid delivery's message = %+v", last)
	}

	// A yes to the held order waits for the merchant; to the unpaid one,
	// for the payment. Each is told so, not that it is confirmed.
	for _, id := range []ulid.ID{held, prepaid} {
		err := pgxTx(ctx, c.pool, func(tx pgx.Tx) error {
			return c.svc.HandleReplyTx(ctx, tx, app.Reply{Kind: domain.OrderConfirmation, OrderID: &id, Answer: domain.Confirm, At: time.Now()})
		})
		if err != nil || c.orders.status(id) != orders.StatusPending {
			t.Fatalf("%s after the yes: %s %v", id, c.orders.status(id), err)
		}
		jobs := c.sendJobs()
		if err = c.svc.SendMessage(ctx, jobs[len(jobs)-1].MessageID, false); err != nil {
			t.Fatal(err)
		}
	}
	sent = c.wa.Sent()
	if a, b := sent[len(sent)-2].Text, sent[len(sent)-1].Text; !strings.Contains(a, "will call you") || !strings.Contains(b, "once its delivery is paid") {
		t.Fatalf("thanks = %q / %q", a, b)
	}
	if got := c.confirmation(ctx, t, prepaid); got.Status != domain.Pending {
		t.Fatalf("the unpaid order's confirmation: %s", got.Status)
	}

	// The SMS link is the prepaid delivery's only way to pay.
	v, err := c.svc.LinkConfirmation(ctx, tokenOf(t, last.Params[4]))
	if err != nil || !v.DepositDue || v.Deposit.Amount() != 6500 || v.OnDelivery.Amount() != 52000 || v.OrderID != prepaid {
		t.Fatalf("link view = %+v, %v", v, err)
	}
	token := tokenOf(t, last.Params[4])
	for _, tt := range []struct {
		ctx   context.Context
		order ulid.ID
		token string
		want  bool
	}{{ctx, prepaid, token, true}, {ctx, held, token, false}, {ctx, prepaid, "nope", false}, {c.store(t), prepaid, token, false}} {
		if ok, vErr := c.svc.ValidPayLink(tt.ctx, tt.order, tt.token); vErr != nil || ok != tt.want {
			t.Errorf("ValidPayLink(%s, %q) = %v %v", tt.order, tt.token, ok, vErr)
		}
	}

	// A store that does not confirm: a verified order is asked, but the
	// thank-you page offers no link; a held one gets nothing; a prepaid
	// one, its link to pay.
	if _, err = c.svc.SaveSettings(ctx, domain.Settings{ConfirmCOD: false, AutoCancelHours: 3}); err != nil {
		t.Fatal(err)
	}
	ver := c.orders.add(t, c.ids, orders.PaymentCOD)
	c.orders.setRisk(ver, orders.RiskVerify)
	held2 := c.orders.add(t, c.ids, orders.PaymentCOD)
	c.orders.setRisk(held2, orders.RiskHold)
	prepaid2 := c.orders.add(t, c.ids, orders.PaymentCOD)
	c.orders.setDeposit(t, prepaid2)
	for _, id := range []ulid.ID{ver, held2, prepaid2} {
		if err = c.svc.ConfirmOrder(ctx, id); err != nil {
			t.Fatal(err)
		}
		if link, lErr := c.svc.WhatsAppLink(ctx, id, "ar"); lErr != nil || link != "" {
			t.Fatalf("%s: a thank-you link where the store does not confirm: %q %v", id, link, lErr)
		}
	}
	if got := c.confirmation(ctx, t, ver); got.Status != domain.Pending || got.MessageID == nil {
		t.Fatalf("a verified order is asked even with confirmation off: %+v", got)
	}
	if _, err = c.svc.Confirmation(ctx, held2); !errors.Is(err, app.ErrConfirmationNotFound) {
		t.Fatalf("a held order's confirmation with confirmation off: %v", err)
	}
	if got := c.confirmation(ctx, t, prepaid2); got.MessageID == nil {
		t.Fatalf("a prepaid order with confirmation off is not sent its link: %+v", got)
	}
	err = pgxTx(ctx, c.pool, func(tx pgx.Tx) error {
		_, _, aErr := c.svc.AnswerTx(ctx, tx, ver, domain.Confirm, domain.ActorWhatsApp, time.Now())
		return aErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.confirmation(ctx, t, ver); got.Status != domain.Confirmed || c.orders.status(ver) != orders.StatusPending {
		t.Fatalf("after the customer's yes: %s, order %s", got.Status, c.orders.status(ver))
	}
	p, err := c.svc.Notifications(ctx, nil, false, 50)
	if err != nil || len(p.Items) == 0 || p.Items[0].Kind != domain.OrderConfirmedNotice || !p.Items[0].Data.NeedsCall {
		t.Fatalf("notices = %+v, %v", p.Items, err)
	}
}

// TestVerifiedYesAwaitsTheCall (ADR-0070): the customer of an order the
// risk rules verify is never told it is confirmed: the WhatsApp thanks
// and the SMS link's page say the store will call.
func TestVerifiedYesAwaitsTheCall(t *testing.T) {
	t.Parallel()
	c := newConfirmFixture(t, options{})
	ctx := c.store(t)
	byWA := c.orders.add(t, c.ids, orders.PaymentCOD)
	c.orders.setRisk(byWA, orders.RiskVerify)
	if err := c.svc.ConfirmOrder(ctx, byWA); err != nil {
		t.Fatal(err)
	}
	err := pgxTx(ctx, c.pool, func(tx pgx.Tx) error {
		return c.svc.HandleReplyTx(ctx, tx, app.Reply{Kind: domain.OrderConfirmation, OrderID: &byWA, Answer: domain.Confirm, At: time.Now()})
	})
	if err != nil {
		t.Fatal(err)
	}
	jobs := c.sendJobs()
	if err = c.svc.SendMessage(ctx, jobs[len(jobs)-1].MessageID, false); err != nil {
		t.Fatal(err)
	}
	sent := c.wa.Sent()
	if last := sent[len(sent)-1]; !strings.Contains(last.Text, "will call you to confirm") || strings.Contains(last.Text, "is confirmed") {
		t.Fatalf("thanks = %q", last.Text)
	}

	c.wa.Unreachable(customer.Unmasked())
	bySMS := c.orders.add(t, c.ids, orders.PaymentCOD)
	c.orders.setRisk(bySMS, orders.RiskVerify)
	if err = c.svc.ConfirmOrder(ctx, bySMS); err != nil {
		t.Fatal(err)
	}
	mid := *c.confirmation(ctx, t, bySMS).MessageID
	for range 2 { // WhatsApp refuses, then the SMS goes
		if err = c.svc.SendMessage(ctx, mid, false); err != nil {
			t.Fatal(err)
		}
	}
	sms := c.sms.Sent()
	token := tokenOf(t, sms[len(sms)-1].Text)
	v, err := c.svc.AnswerLink(ctx, token, domain.Confirm)
	if err != nil || v.Status != domain.Confirmed || !v.AwaitingCall {
		t.Fatalf("answer = %+v, %v", v, err)
	}
	if v, err = c.svc.LinkConfirmation(ctx, token); err != nil || !v.AwaitingCall {
		t.Fatalf("view after = %+v, %v", v, err)
	}
	// Once the store confirms on the call, the page says so.
	c.orders.set(bySMS, orders.StatusConfirmed)
	if v, err = c.svc.LinkConfirmation(ctx, token); err != nil || v.AwaitingCall {
		t.Fatalf("view after the call = %+v, %v", v, err)
	}
}
