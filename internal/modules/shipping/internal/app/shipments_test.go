package app_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/carrier"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/carrier/fake"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/media"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// fakeOrders keeps orders in memory and moves them only along the
// edges a carrier makes; moves happen when the caller's tx commits.
type fakeOrders struct {
	mu     sync.Mutex
	orders map[ulid.ID]*app.OrderInfo
	moves  []string // "<order> <to> <actor>"
	refuse bool     // refuse every move (the merchant cancelled the order)
}

func (o *fakeOrders) add(t *testing.T, status string) ulid.ID {
	t.Helper()
	cod, err := money.New(52000, money.EGP)
	if err != nil {
		t.Fatal(err)
	}
	id := ulid.NewGenerator().New()
	o.mu.Lock()
	defer o.mu.Unlock()
	o.orders[id] = &app.OrderInfo{ID: id, Status: status, Name: "منى أحمد", Phone: "+201012345678", Governorate: "EG-C",
		City: "مدينة نصر", Address: "12 شارع عباس العقاد", Notes: "اتصل قبل الوصول", COD: cod, Items: 2, Description: "عطر × 2"}
	return id
}

func (o *fakeOrders) Order(_ context.Context, id ulid.ID) (app.OrderInfo, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	info, ok := o.orders[id]
	if !ok {
		return app.OrderInfo{}, app.ErrOrderUnknown
	}
	return *info, nil
}

func (o *fakeOrders) MoveTx(_ context.Context, _ pgx.Tx, id ulid.ID, to, actor, _ string) (bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.refuse {
		return false, app.ErrOrderMoveRefused
	}
	info := o.orders[id]
	if info.Status == to {
		return false, nil
	}
	info.Status = to
	o.moves = append(o.moves, id.String()[20:]+" "+to+" "+actor)
	return true, nil
}

func (o *fakeOrders) status(id ulid.ID) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.orders[id].Status
}

// captureJobs keeps the jobs enqueued in committed transactions.
type captureJobs struct {
	mu   sync.Mutex
	jobs []river.JobArgs
	opts []*river.InsertOpts
}

func (c *captureJobs) InsertTx(_ context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	if tx == nil {
		return nil, errors.New("jobs must be enqueued in a transaction")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.jobs = append(c.jobs, args)
	c.opts = append(c.opts, opts)
	return &rivertype.JobInsertResult{}, nil
}

func (c *captureJobs) kinds() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.jobs))
	for i, j := range c.jobs {
		out[i] = j.Kind()
	}
	return out
}

// memObjects is an in-memory object store.
type memObjects struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (o *memObjects) PresignUpload(context.Context, string, string, int64, time.Duration) (media.Upload, error) {
	return media.Upload{}, errors.New("not used")
}

func (o *memObjects) Get(_ context.Context, key string, _ int64) ([]byte, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	b, ok := o.m[key]
	if !ok {
		return nil, errors.New("no such object")
	}
	return b, nil
}

func (o *memObjects) Put(_ context.Context, key, _ string, data []byte) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.m[key] = data
	return nil
}

func (o *memObjects) Delete(_ context.Context, key string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.m, key)
	return nil
}

// memInbox is the webhooks inbox in memory.
type memInbox struct {
	mu       sync.Mutex
	receipts map[ulid.ID]webhooks.Receipt
	bodies   map[ulid.ID][]byte
	outcome  map[ulid.ID]string
}

func (m *memInbox) Get(_ context.Context, id ulid.ID) (webhooks.Receipt, string, []byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.receipts[id]
	if !ok {
		return webhooks.Receipt{}, "", nil, webhooks.ErrReceiptNotFound
	}
	return r, "application/json", m.bodies[id], nil
}

func (m *memInbox) MarkProcessedTx(_ context.Context, _ pgx.Tx, id ulid.ID, outcome string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, done := m.outcome[id]; done {
		return false, nil
	}
	m.outcome[id] = outcome
	return true, nil
}

func (m *memInbox) put(provider string, body []byte) ulid.ID {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := ulid.NewGenerator().New()
	m.receipts[id] = webhooks.Receipt{ID: id, Provider: provider}
	m.bodies[id] = body
	return id
}

type shipFixture struct {
	fixture
	svc     *app.Service
	carrier *fake.Carrier
	orders  *fakeOrders
	jobs    *captureJobs
	objects *memObjects
	inbox   *memInbox
	conn    app.Connected
}

func newShipFixture(t *testing.T) shipFixture {
	t.Helper()
	f := shipFixture{fixture: newFixture(t), carrier: fake.New(), orders: &fakeOrders{orders: map[ulid.ID]*app.OrderInfo{}},
		jobs: &captureJobs{}, objects: &memObjects{m: map[string][]byte{}},
		inbox: &memInbox{receipts: map[ulid.ID]webhooks.Receipt{}, bodies: map[ulid.ID][]byte{}, outcome: map[ulid.ID]string{}}}
	f.svc = f.newService(f.carrier)
	var err error
	if f.conn, err = f.svc.Connect(f.ctxA, fake.Name, "merchant-key"); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f shipFixture) newService(c carrier.Carrier) *app.Service {
	return app.NewService(app.Deps{DB: f.pool, IDs: f.ids, Audit: f.rec, Events: f.rec, Cache: app.DefaultCacheConfig(),
		Carriers: app.Carriers{fake.Name: c}, Sealer: sealer(f.t), PublicURL: "https://app.example.com",
		Orders: f.orders, Jobs: f.jobs, Objects: f.objects, Inbox: f.inbox,
		Stores: func(context.Context) (app.StoreInfo, error) {
			return app.StoreInfo{Currency: "EGP", Country: "EG"}, nil
		}})
}

// ship ships one order and runs its create job.
func (f shipFixture) ship(t *testing.T, order ulid.ID) domain.Shipment {
	t.Helper()
	res, err := f.svc.Ship(f.ctxA, []ulid.ID{order}, f.conn.Connection.ID)
	if err != nil || len(res) != 1 || res[0].Err != nil {
		t.Fatalf("ship: %+v %v", res, err)
	}
	if err = f.svc.CreateShipment(f.ctxA, res[0].Shipment.ID); err != nil {
		t.Fatal(err)
	}
	sh, _, err := f.svc.Shipment(f.ctxA, res[0].Shipment.ID)
	if err != nil {
		t.Fatal(err)
	}
	return sh
}

func TestShip(t *testing.T) {
	t.Parallel()
	f := newShipFixture(t)
	ready := f.orders.add(t, "ready_to_ship")
	pending := f.orders.add(t, "pending")
	res, err := f.svc.Ship(f.ctxA, []ulid.ID{ready, pending, ulid.NewGenerator().New(), ready}, f.conn.Connection.ID)
	if err != nil || len(res) != 3 {
		t.Fatalf("ship: %+v %v", res, err)
	}
	if res[0].Err != nil || res[0].Shipment.Status != domain.ShipmentRequested || res[0].Shipment.COD.Amount() != 52000 {
		t.Fatalf("ready order: %+v", res[0])
	}
	for i, code := range []string{"", "order_not_ready", "order_not_found"} {
		var e *errs.Error
		if i > 0 && (!errors.As(res[i].Err, &e) || e.Code != code) {
			t.Errorf("result %d: %v, want %s", i, res[i].Err, code)
		}
	}
	if got := f.jobs.kinds(); len(got) != 1 || got[0] != "shipping.create_shipment" {
		t.Fatalf("jobs %v", got)
	}
	again, err := f.svc.Ship(f.ctxA, []ulid.ID{ready}, f.conn.Connection.ID)
	var e *errs.Error
	if err != nil || !errors.As(again[0].Err, &e) || e.Code != "order_already_shipped" {
		t.Fatalf("second shipment of one order: %+v %v", again, err)
	}
	if _, err = f.svc.Ship(f.ctxA, []ulid.ID{ready}, ulid.NewGenerator().New()); !fieldIs(err, "connection_id", "not_found") {
		t.Fatalf("unknown connection: %v", err)
	}
	if _, err = f.svc.Ship(f.ctxA, nil, f.conn.Connection.ID); !fieldIs(err, "order_ids", "count") {
		t.Fatalf("no orders: %v", err)
	}
	// Store B cannot ship with store A's connection.
	if _, err = f.svc.Ship(f.ctxB, []ulid.ID{ready}, f.conn.Connection.ID); !fieldIs(err, "connection_id", "not_found") {
		t.Fatalf("B uses A's connection: %v", err)
	}
}

// TestCreateShipment: the job hands the order to the carrier with our
// shipment ID as the reference, records the tracking number, fetches the
// label and schedules the poll; it is safe to run twice.
func TestCreateShipment(t *testing.T) {
	t.Parallel()
	f := newShipFixture(t)
	order := f.orders.add(t, "ready_to_ship")
	sh := f.ship(t, order)
	if sh.Status != domain.ShipmentCreated || sh.TrackingNumber == "" {
		t.Fatalf("created: %+v", sh)
	}
	req, ok := f.carrier.Request(sh.TrackingNumber)
	if !ok || req.Reference != sh.ID.String() || req.COD.Amount() != 52000 || req.Receiver.Phone != "+201012345678" ||
		req.Address.Governorate != "EG-C" || req.Address.Line != "12 شارع عباس العقاد" || req.Items != 2 {
		t.Fatalf("carrier request %+v", req)
	}
	kinds := f.jobs.kinds()
	if len(kinds) != 3 || kinds[1] != "shipping.fetch_label" || kinds[2] != "shipping.track_shipment" ||
		f.jobs.opts[2] == nil || time.Until(f.jobs.opts[2].ScheduledAt) < app.PollEvery-time.Minute {
		t.Fatalf("jobs %v %+v", kinds, f.jobs.opts)
	}
	if err := f.svc.CreateShipment(f.ctxA, sh.ID); err != nil || f.carrier.Calls("create") != 1 {
		t.Fatalf("second run: %v, %d creates", err, f.carrier.Calls("create"))
	}

	if _, err := f.svc.Label(f.ctxA, sh.ID); !errors.Is(err, app.ErrLabelNotReady) {
		t.Fatalf("label before fetch: %v", err)
	}
	if err := f.svc.FetchLabel(f.ctxA, sh.ID); err != nil {
		t.Fatal(err)
	}
	pdf, err := f.svc.Label(f.ctxA, sh.ID)
	storeA, _ := tenant.FromContext(f.ctxA)
	if err != nil || !strings.HasPrefix(string(pdf), "%PDF") {
		t.Fatalf("label: %q %v", pdf, err)
	}
	for key := range f.objects.m {
		if !strings.HasPrefix(key, storeA.ObjectPrefix()+"shipping/labels/") {
			t.Fatalf("label stored outside the store's prefix: %s", key)
		}
	}
	if _, err = f.svc.Label(f.ctxB, sh.ID); !errors.Is(err, app.ErrShipmentNotFound) {
		t.Fatalf("B reads A's label: %v", err)
	}
}

// refusing is a carrier that refuses every shipment.
type refusing struct{ *fake.Carrier }

func (refusing) CreateShipment(context.Context, carrier.Credentials, carrier.ShipmentRequest) (carrier.Shipment, error) {
	return carrier.Shipment{}, carrier.ErrRejected
}

func TestCreateShipmentRefused(t *testing.T) {
	t.Parallel()
	f := newShipFixture(t)
	svc := f.newService(refusing{f.carrier})
	order := f.orders.add(t, "ready_to_ship")
	res, err := svc.Ship(f.ctxA, []ulid.ID{order}, f.conn.Connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.CreateShipment(f.ctxA, res[0].Shipment.ID); err != nil {
		t.Fatal(err)
	}
	sh, evs, err := svc.Shipment(f.ctxA, res[0].Shipment.ID)
	if err != nil || sh.Status != domain.ShipmentFailed || sh.Failure == "" || len(evs) != 2 {
		t.Fatalf("refused: %+v %d events %v", sh, len(evs), err)
	}
	// A failed shipment frees the order for another try.
	if again, shipErr := svc.Ship(f.ctxA, []ulid.ID{order}, f.conn.Connection.ID); shipErr != nil || again[0].Err != nil {
		t.Fatalf("ship again after failure: %+v %v", again, shipErr)
	}
}

// TestTrackingMovesTheOrder: polls follow the parcel, the order moves
// with it (along a path when it skips states), a repeat changes nothing,
// and the poll stops when the shipment is final.
func TestTrackingMovesTheOrder(t *testing.T) {
	t.Parallel()
	f := newShipFixture(t)
	order := f.orders.add(t, "ready_to_ship")
	sh := f.ship(t, order)

	again, err := f.svc.TrackShipment(f.ctxA, sh.ID)
	if err != nil || !again || f.orders.status(order) != "ready_to_ship" {
		t.Fatalf("nothing moved yet: again=%v %v, order %s", again, err, f.orders.status(order))
	}
	if err = f.carrier.Advance(sh.TrackingNumber, carrier.StatusDelivered); err != nil {
		t.Fatal(err)
	}
	if again, err = f.svc.TrackShipment(f.ctxA, sh.ID); err != nil || again {
		t.Fatalf("delivered: again=%v %v", again, err)
	}
	if f.orders.status(order) != "delivered" || len(f.orders.moves) != 2 || !strings.HasSuffix(f.orders.moves[0], "shipped carrier:fake") {
		t.Fatalf("order %s, moves %v", f.orders.status(order), f.orders.moves)
	}
	if again, err = f.svc.TrackShipment(f.ctxA, sh.ID); err != nil || again || len(f.orders.moves) != 2 {
		t.Fatalf("after final: again=%v %v, moves %v", again, err, f.orders.moves)
	}
	got, evs, err := f.svc.Shipment(f.ctxA, sh.ID)
	if err != nil || got.Status != domain.ShipmentDelivered || len(evs) != 3 || evs[2].Source != domain.SourcePoll {
		t.Fatalf("shipment %+v events %+v %v", got, evs, err)
	}
	if _, events := f.rec.counts(); events != 1 {
		t.Fatalf("status events published = %d, want 1", events)
	}
}

func TestOrderRefusesToMove(t *testing.T) {
	t.Parallel()
	f := newShipFixture(t)
	order := f.orders.add(t, "ready_to_ship")
	sh := f.ship(t, order)
	f.orders.refuse = true
	if err := f.carrier.Advance(sh.TrackingNumber, carrier.StatusInTransit); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.TrackShipment(f.ctxA, sh.ID); err != nil {
		t.Fatal(err)
	}
	got, evs, err := f.svc.Shipment(f.ctxA, sh.ID)
	if err != nil || got.Status != domain.ShipmentInTransit || !strings.Contains(evs[len(evs)-1].Note, "not moved") {
		t.Fatalf("the shipment still follows the parcel: %+v %+v %v", got, evs, err)
	}
}

func TestCancelShipment(t *testing.T) {
	t.Parallel()
	f := newShipFixture(t)
	// Before the carrier knows it: cancelled at once, the create job
	// then does nothing.
	early := f.orders.add(t, "ready_to_ship")
	res, err := f.svc.Ship(f.ctxA, []ulid.ID{early}, f.conn.Connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := f.svc.Cancel(f.ctxA, res[0].Shipment.ID)
	if err != nil || sh.Status != domain.ShipmentCancelled {
		t.Fatalf("early cancel: %+v %v", sh, err)
	}
	if err = f.svc.CreateShipment(f.ctxA, sh.ID); err != nil || f.carrier.Calls("create") != 0 {
		t.Fatalf("create after cancel: %v, %d creates", err, f.carrier.Calls("create"))
	}
	if _, err = f.svc.Cancel(f.ctxA, sh.ID); !errors.Is(err, app.ErrNotCancellable) {
		t.Fatalf("cancel a cancelled shipment: %v", err)
	}

	// Created: the cancel job asks the carrier.
	created := f.ship(t, f.orders.add(t, "ready_to_ship"))
	if sh, err = f.svc.Cancel(f.ctxA, created.ID); err != nil || sh.CancelRequestedAt == nil || sh.Status != domain.ShipmentCreated {
		t.Fatalf("cancel requested: %+v %v", sh, err)
	}
	if err = f.svc.CancelShipment(f.ctxA, created.ID); err != nil {
		t.Fatal(err)
	}
	if sh, _, err = f.svc.Shipment(f.ctxA, created.ID); err != nil || sh.Status != domain.ShipmentCancelled {
		t.Fatalf("after the cancel job: %+v %v", sh, err)
	}

	// On its way: the carrier refuses; the timeline says so.
	moving := f.ship(t, f.orders.add(t, "ready_to_ship"))
	if err = f.carrier.Advance(moving.TrackingNumber, carrier.StatusInTransit); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.Cancel(f.ctxA, moving.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.CancelShipment(f.ctxA, moving.ID); err != nil {
		t.Fatal(err)
	}
	sh, evs, err := f.svc.Shipment(f.ctxA, moving.ID)
	if err != nil || sh.Status == domain.ShipmentCancelled || !strings.Contains(evs[len(evs)-1].Note, "refused to cancel") {
		t.Fatalf("too late: %+v %+v %v", sh, evs, err)
	}
	if _, err = f.svc.Cancel(f.ctxB, moving.ID); !errors.Is(err, app.ErrShipmentNotFound) {
		t.Fatalf("B cancels A's shipment: %v", err)
	}
}

func webhookReq(secretHeader, secretQuery string, body []byte) webhooks.Request {
	h := http.Header{}
	if secretHeader != "" {
		h.Set(app.SecretHeader, secretHeader)
	}
	q := url.Values{}
	if secretQuery != "" {
		q.Set("secret", secretQuery)
	}
	return webhooks.Request{Header: h, Query: q, Body: body}
}

// TestWebhooks: the provider checks the connection's secret (header or
// query) and reads the event id; the job re-queries the carrier and
// follows its answer, not the webhook's.
func TestWebhooks(t *testing.T) {
	t.Parallel()
	f := newShipFixture(t)
	order := f.orders.add(t, "ready_to_ship")
	sh := f.ship(t, order)
	p := f.svc.WebhookProviders()[0]
	if p.Name() != fake.Name {
		t.Fatalf("provider %s", p.Name())
	}
	if err := f.carrier.Advance(sh.TrackingNumber, carrier.StatusOutForDelivery); err != nil {
		t.Fatal(err)
	}
	body, err := f.carrier.Webhook(sh.TrackingNumber)
	if err != nil {
		t.Fatal(err)
	}
	conn := f.conn.Connection.ID
	for name, req := range map[string]webhooks.Request{"header": webhookReq(f.conn.WebhookSecret, "", body), "query": webhookReq("", f.conn.WebhookSecret, body)} {
		if v, verr := p.Verify(f.ctxA, conn, req); verr != nil || v.EventID == "" {
			t.Errorf("%s secret: %+v %v", name, v, verr)
		}
	}
	if _, err = p.Verify(f.ctxA, conn, webhookReq("wrong", "", body)); !errors.Is(err, webhooks.ErrForged) {
		t.Errorf("wrong secret: %v", err)
	}
	if _, err = p.Verify(f.ctxA, conn, webhookReq("", "", body)); !errors.Is(err, webhooks.ErrForged) {
		t.Errorf("no secret: %v", err)
	}
	if _, err = p.Verify(f.ctxB, conn, webhookReq(f.conn.WebhookSecret, "", body)); !errors.Is(err, webhooks.ErrUnknownConnection) {
		t.Errorf("A's connection under B: %v", err)
	}
	if v, verr := p.Verify(f.ctxA, conn, webhookReq(f.conn.WebhookSecret, "", []byte(`{}`))); verr != nil || v.EventID != "" {
		t.Errorf("unreadable body: %+v %v (want no event id: the inbox refuses it)", v, verr)
	}

	// The webhook says out for delivery, but by now the carrier says
	// delivered: the job follows the carrier.
	if err = f.carrier.Advance(sh.TrackingNumber, carrier.StatusDelivered); err != nil {
		t.Fatal(err)
	}
	r := f.inbox.put(fake.Name, body)
	if err = f.svc.ProcessWebhook(f.ctxA, r); err != nil {
		t.Fatal(err)
	}
	got, evs, err := f.svc.Shipment(f.ctxA, sh.ID)
	if err != nil || got.Status != domain.ShipmentDelivered || evs[len(evs)-1].Source != domain.SourceWebhook || f.inbox.outcome[r] != "updated" {
		t.Fatalf("after the webhook: %+v %v outcome %q", got, err, f.inbox.outcome[r])
	}
	if f.orders.status(order) != "delivered" {
		t.Fatalf("order %s", f.orders.status(order))
	}
	unknown := f.inbox.put(fake.Name, []byte(`{"event_id":"e","tracking_number":"FK999999"}`))
	if err = f.svc.ProcessWebhook(f.ctxA, unknown); err != nil || f.inbox.outcome[unknown] != "unknown_shipment" {
		t.Fatalf("unknown shipment: %v %q", err, f.inbox.outcome[unknown])
	}
	other := f.inbox.put(fake.Name, body)
	if err = f.svc.ProcessWebhook(f.ctxB, other); err != nil || f.inbox.outcome[other] != "unknown_shipment" {
		t.Fatalf("A's tracking number in B's webhook: %v %q", err, f.inbox.outcome[other])
	}
	if err = f.svc.ProcessWebhook(f.ctxA, ulid.NewGenerator().New()); err != nil {
		t.Fatalf("a missing receipt ends the job: %v", err)
	}
}

func TestShipmentsList(t *testing.T) {
	t.Parallel()
	f := newShipFixture(t)
	var ids []ulid.ID
	for range 3 {
		ids = append(ids, f.ship(t, f.orders.add(t, "ready_to_ship")).ID)
	}
	p, err := f.svc.Shipments(f.ctxA, domain.ShipmentQuery{Limit: 2})
	if err != nil || len(p.Items) != 2 || p.Next == nil || p.Items[0].ID != ids[2] {
		t.Fatalf("page 1: %+v %v", p, err)
	}
	p, err = f.svc.Shipments(f.ctxA, domain.ShipmentQuery{Limit: 2, After: p.Next})
	if err != nil || len(p.Items) != 1 || p.Next != nil || p.Items[0].ID != ids[0] {
		t.Fatalf("page 2: %+v %v", p, err)
	}
	st := domain.ShipmentCreated
	order := p.Items[0].OrderID
	if p, err = f.svc.Shipments(f.ctxA, domain.ShipmentQuery{OrderID: &order, Status: &st}); err != nil || len(p.Items) != 1 {
		t.Fatalf("by order and status: %+v %v", p, err)
	}
	if p, err = f.svc.Shipments(f.ctxB, domain.ShipmentQuery{}); err != nil || len(p.Items) != 0 {
		t.Fatalf("B lists A's shipments: %+v %v", p, err)
	}
	if _, err = f.svc.Shipments(f.ctxA, domain.ShipmentQuery{Limit: 201}); !fieldIs(err, "limit", "range") {
		t.Fatalf("limit: %v", err)
	}
	if _, _, err = f.svc.Shipment(f.ctxB, ids[0]); !errors.Is(err, app.ErrShipmentNotFound) {
		t.Fatalf("B reads A's shipment: %v", err)
	}
}

func TestOrderPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		order string
		st    domain.ShipmentStatus
		want  string
	}{
		{"ready_to_ship", domain.ShipmentInTransit, "shipped"},
		{"ready_to_ship", domain.ShipmentDelivered, "shipped delivered"},
		{"ready_to_ship", domain.ShipmentReturned, "shipped failed_delivery returned"},
		{"shipped", domain.ShipmentOutForDelivery, ""},
		{"shipped", domain.ShipmentFailedAttempt, "failed_delivery"},
		{"failed_delivery", domain.ShipmentOutForDelivery, "shipped"},
		{"failed_delivery", domain.ShipmentReturned, "returned"},
		{"delivered", domain.ShipmentReturned, "returned"},
		{"delivered", domain.ShipmentDelivered, ""},
		{"cancelled", domain.ShipmentDelivered, ""},
		{"ready_to_ship", domain.ShipmentCreated, ""},
		{"ready_to_ship", domain.ShipmentCancelled, ""},
	}
	for _, tt := range tests {
		if got := strings.Join(domain.OrderPath(tt.order, tt.st), " "); got != tt.want {
			t.Errorf("OrderPath(%s, %s) = %q, want %q", tt.order, tt.st, got, tt.want)
		}
	}
	if !domain.ShipmentDelivered.Final() || domain.ShipmentInTransit.Final() || domain.ShipmentFailed.Live() || !domain.ShipmentRequested.Live() {
		t.Fatal("status predicates")
	}
}

// TestDisconnectedCarrier: jobs of a shipment whose carrier was
// disconnected end without calling it; a requested one fails.
func TestDisconnectedCarrier(t *testing.T) {
	t.Parallel()
	f := newShipFixture(t)
	created := f.ship(t, f.orders.add(t, "ready_to_ship"))
	res, err := f.svc.Ship(f.ctxA, []ulid.ID{f.orders.add(t, "ready_to_ship")}, f.conn.Connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	requested := res[0].Shipment.ID
	if err = f.svc.Disconnect(f.ctxA, f.conn.Connection.ID); err != nil {
		t.Fatal(err)
	}
	calls := f.carrier.Calls("track") + f.carrier.Calls("label") + f.carrier.Calls("cancel")
	if again, trackErr := f.svc.TrackShipment(f.ctxA, created.ID); trackErr != nil || again {
		t.Fatalf("track: %v %v", again, trackErr)
	}
	if err = f.svc.FetchLabel(f.ctxA, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.Cancel(f.ctxA, created.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.CancelShipment(f.ctxA, created.ID); err != nil {
		t.Fatal(err)
	}
	if now := f.carrier.Calls("track") + f.carrier.Calls("label") + f.carrier.Calls("cancel"); now != calls {
		t.Fatalf("a disconnected carrier was called %d times", now-calls)
	}
	if err = f.svc.CreateShipment(f.ctxA, requested); err != nil {
		t.Fatal(err)
	}
	sh, _, err := f.svc.Shipment(f.ctxA, requested)
	if err != nil || sh.Status != domain.ShipmentFailed || !strings.Contains(sh.Failure, "disconnected") {
		t.Fatalf("requested after disconnect: %+v %v", sh, err)
	}
	body, err := f.carrier.Webhook(created.TrackingNumber)
	if err != nil {
		t.Fatal(err)
	}
	r := f.inbox.put(fake.Name, body)
	if err = f.svc.ProcessWebhook(f.ctxA, r); err != nil || f.inbox.outcome[r] != "disconnected" {
		t.Fatalf("webhook after disconnect: %v %q", err, f.inbox.outcome[r])
	}
	bad := f.inbox.put(fake.Name, []byte(`not json`))
	if err = f.svc.ProcessWebhook(f.ctxA, bad); err != nil || f.inbox.outcome[bad] != "unreadable" {
		t.Fatalf("unreadable: %v %q", err, f.inbox.outcome[bad])
	}
	gone := ulid.NewGenerator().New()
	for name, run := range map[string]func() error{
		"create": func() error { return f.svc.CreateShipment(f.ctxA, gone) },
		"label":  func() error { return f.svc.FetchLabel(f.ctxA, gone) },
		"cancel": func() error { return f.svc.CancelShipment(f.ctxA, gone) },
		"track":  func() error { _, e := f.svc.TrackShipment(f.ctxA, gone); return e },
	} {
		if err = run(); err != nil {
			t.Errorf("%s of a missing shipment: %v", name, err)
		}
	}
}

func TestWorkersRegister(t *testing.T) {
	t.Parallel()
	f := newShipFixture(t)
	w := river.NewWorkers()
	if err := f.svc.AddWorkers(w); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.AddWorkers(w); err == nil {
		t.Fatal("workers registered twice")
	}
}
