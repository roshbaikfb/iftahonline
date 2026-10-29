package app_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/capi"
	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// queue keeps the conversions Track enqueued.
type queue struct {
	mu   sync.Mutex
	args []app.ConversionArgs
}

func (q *queue) InsertTx(_ context.Context, _ pgx.Tx, a river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.args = append(q.args, a.(app.ConversionArgs))
	return &rivertype.JobInsertResult{}, nil
}

func (q *queue) take() []app.ConversionArgs {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := q.args
	q.args = nil
	return out
}

// sent keeps what each provider was sent, and fails with err.
type sent struct {
	mu     sync.Mutex
	pixels []capi.Pixel
	events []capi.Event
	err    error
}

func (s *sent) Send(_ context.Context, p capi.Pixel, e capi.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pixels, s.events = append(s.pixels, p), append(s.events, e)
	return s.err
}

type orders struct {
	p  app.Purchase
	ok bool
}

func (o orders) Purchase(context.Context, ulid.ID) (app.Purchase, bool, error) { return o.p, o.ok, nil }

func (f fixture) withConversions(q *queue, s *sent, o app.Orders) *app.Service {
	return app.NewService(app.Deps{DB: f.pool, IDs: f.ids, Sealer: f.sealer, Events: f.pub, Jobs: q, Orders: o,
		Senders: map[domain.Provider]capi.Sender{domain.Meta: s, domain.TikTok: s, domain.Snap: s, domain.GA4: s}})
}

func egp(t *testing.T, minor int64) money.Money {
	t.Helper()
	m, err := money.New(minor, money.EGP)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestTrack: a conversion goes to each provider the store sends server
// events to (enabled, with a server API and a token), sealed; none
// otherwise.
func TestTrack(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.store(t)
	q, s := &queue{}, &sent{}
	svc := f.withConversions(q, s, nil)
	c := app.Conversion{Name: capi.AddToCart, EventID: "01M6EVENT", Time: time.Unix(1_761_700_000, 0), Value: egp(t, 30000), Items: []string{"v1"},
		User: capi.User{IP: "203.0.113.7", UserAgent: "Mozilla/5.0", FBP: "fb.1.2.3", Phone: "+201012345678"}}
	if err := svc.Track(ctx, c); err != nil || len(q.take()) != 0 {
		t.Fatal("a store without pixels enqueued conversions")
	}
	for p, in := range map[domain.Provider]domain.PixelInput{
		domain.Meta:      {PixelID: metaID, Enabled: true, ServerToken: ptr("meta-token-1")},
		domain.Snap:      {PixelID: "0f8fad5b-d9cb-469f-a165-70867728950e", Enabled: true, ServerToken: ptr("snap-token-1")},
		domain.TikTok:    {PixelID: "C4ABCDEF1234567890AB", Enabled: true},                          // no token
		domain.GA4:       {PixelID: "G-ABC123XYZ", Enabled: false, ServerToken: ptr("ga-secret-1")}, // off
		domain.GoogleAds: {PixelID: "AW-123456789", Label: "AbC-123_xyz", Enabled: true},            // no server API
	} {
		if _, err := svc.SavePixel(ctx, p, in); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.Track(ctx, c); err != nil {
		t.Fatal(err)
	}
	got := q.take()
	if len(got) != 2 || got[0].Provider != domain.Meta || got[1].Provider != domain.Snap {
		t.Fatalf("enqueued %+v", got)
	}
	for _, a := range got {
		if a.EventID != "01M6EVENT" || a.Name != capi.AddToCart || a.ValueMinor != 30000 || a.Currency != "EGP" || a.User.IP != "203.0.113.7" ||
			a.User.Phone != "" { // a phone never waits in the job table
			t.Fatalf("args %+v", a)
		}
	}

	// The worker sends each with the provider's token and the user.
	for _, a := range got {
		if err := svc.SendConversion(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.events) != 2 || s.pixels[0].Token != "meta-token-1" || s.pixels[1].Token != "snap-token-1" || s.events[0].ID != "01M6EVENT" ||
		s.events[0].User.IP != "203.0.113.7" || s.events[0].Units != 2 || !s.events[0].Time.Equal(c.Time) {
		t.Fatalf("sent %+v %+v", s.pixels, s.events)
	}

	// A Purchase names an order; a role without the orders cannot send it.
	purchase := got[0]
	purchase.Name, purchase.OrderID = capi.Purchase, f.ids.New().String()
	var cancel *river.JobCancelError
	if err := svc.SendConversion(ctx, purchase); !errors.As(err, &cancel) {
		t.Fatalf("purchase without orders: %v", err)
	}
	purchase.OrderID = "not-an-order"
	if err := f.withConversions(q, s, orders{}).SendConversion(ctx, purchase); !errors.As(err, &cancel) {
		t.Fatalf("purchase of a bad order ID: %v", err)
	}
}

// TestSendConversion: a pixel the merchant removed or turned off since
// is dropped; a refusal for good cancels the job; anything else retries.
func TestSendConversion(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.store(t)
	q, s := &queue{}, &sent{}
	svc := f.withConversions(q, s, nil)
	p, err := svc.SavePixel(ctx, domain.Meta, domain.PixelInput{PixelID: metaID, Enabled: true, ServerToken: ptr("meta-token-1")})
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.Track(ctx, app.Conversion{Name: capi.InitiateCheckout, EventID: "01M6CHECKOUT", Time: time.Now(), Value: egp(t, 100)}); err != nil {
		t.Fatal(err)
	}
	a := q.take()[0]

	s.err = capi.ErrRejected
	var cancel *river.JobCancelError
	if err = svc.SendConversion(ctx, a); !errors.As(err, &cancel) {
		t.Fatalf("rejected: %v", err)
	}
	s.err = errors.New("timeout")
	if err = svc.SendConversion(ctx, a); err == nil || errors.As(err, &cancel) {
		t.Fatalf("transient: %v", err)
	}
	s.err = nil

	if _, err = svc.SavePixel(ctx, domain.Meta, domain.PixelInput{PixelID: metaID, Enabled: false, Version: p.Version}); err != nil {
		t.Fatal(err)
	}
	n := len(s.events)
	if err = svc.SendConversion(ctx, a); err != nil || len(s.events) != n {
		t.Fatalf("turned off: %v, sent %d", err, len(s.events)-n)
	}
	if err = svc.DeletePixel(ctx, domain.Meta); err != nil {
		t.Fatal(err)
	}
	if err = svc.SendConversion(ctx, a); err != nil || len(s.events) != n {
		t.Fatalf("removed: %v", err)
	}
	a.Provider = domain.GoogleAds // no sender
	if err = svc.SendConversion(ctx, a); !errors.As(err, &cancel) {
		t.Fatalf("no sender: %v", err)
	}
}

// TestTrackPurchase: an order's Purchase carries its event_id, value and
// who it matches; an order without signals is not reported.
func TestTrackPurchase(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.store(t)
	if _, err := f.svc.SavePixel(ctx, domain.Meta, domain.PixelInput{PixelID: metaID, Enabled: true, ServerToken: ptr("meta-token-1")}); err != nil {
		t.Fatal(err)
	}
	order := f.ids.New()
	p := app.Purchase{OrderID: order, PlacedAt: time.Unix(1_761_700_000, 0), Total: egp(t, 45000), Items: []string{"v1", "v2"},
		Phone: "+201012345678", CustomerID: "C1", Country: "EG", Signals: capi.User{IP: "203.0.113.7", FBC: "fb.1.2.c"}}
	q, s := &queue{}, &sent{}
	if err := f.withConversions(q, s, orders{p: p}).TrackPurchase(ctx, order); err != nil || len(q.take()) != 0 {
		t.Fatalf("an order without signals was reported: %v", err)
	}
	svc := f.withConversions(q, s, orders{p: p, ok: true})
	if err := svc.TrackPurchase(ctx, order); err != nil {
		t.Fatal(err)
	}
	got := q.take()
	if len(got) != 1 || got[0].EventID != "purchase:"+order.String() || got[0].OrderID != order.String() || got[0].ValueMinor != 45000 ||
		got[0].User != (capi.User{}) {
		t.Fatalf("enqueued %+v", got)
	}
	if err := svc.SendConversion(ctx, got[0]); err != nil {
		t.Fatal(err)
	}
	u := s.events[0].User
	if u.Phone != "+201012345678" || u.ExternalID != "C1" || u.Country != "EG" || u.IP != "203.0.113.7" || u.FBC != "fb.1.2.c" {
		t.Fatalf("user %+v", u)
	}
	// Signals dropped since (their month passed): nothing is sent.
	n := len(s.events)
	if err := f.withConversions(q, s, orders{p: p}).SendConversion(ctx, got[0]); err != nil || len(s.events) != n {
		t.Fatalf("signals gone: %v, sent %d", err, len(s.events)-n)
	}
	if app.PurchaseEventID(order) != "purchase:"+order.String() {
		t.Fatal("PurchaseEventID")
	}
}

// TestNoSealer: the storefront role holds no keys: it enqueues, but
// never saves a token or sends.
func TestNoSealer(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.store(t)
	if _, err := f.svc.SavePixel(ctx, domain.Meta, domain.PixelInput{PixelID: metaID, Enabled: true, ServerToken: ptr("meta-token-1")}); err != nil {
		t.Fatal(err)
	}
	q := &queue{}
	keyless := app.NewService(app.Deps{DB: f.pool, IDs: f.ids, Events: f.pub, Jobs: q})
	if err := keyless.Track(ctx, app.Conversion{Name: capi.AddToCart, EventID: "e1", Time: time.Now(), Value: egp(t, 100)}); err != nil {
		t.Fatal(err)
	}
	got := q.take()
	if len(got) != 1 {
		t.Fatalf("enqueued %+v", got)
	}
	if err := keyless.SendConversion(ctx, got[0]); err == nil {
		t.Fatal("sent without keys")
	}
	if _, err := keyless.SavePixel(ctx, domain.Snap, domain.PixelInput{PixelID: "0f8fad5b-d9cb-469f-a165-70867728950e"}); err == nil {
		t.Fatal("saved a pixel without keys")
	}
}
