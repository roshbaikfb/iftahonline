// Package shiptest has in-memory stand-ins for the shipping module's
// dependencies (orders, jobs, object storage, the webhooks inbox), shared
// by the module's tests.
package shiptest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/webhooks"
	"github.com/roshbaikfb/iftahonline/internal/platform/media"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// NewOrders returns an empty order store.
func NewOrders() *Orders { return &Orders{orders: map[ulid.ID]*app.OrderInfo{}} }

// NewJobs returns an empty job capture.
func NewJobs() *Jobs { return &Jobs{} }

// NewObjects returns an empty object store.
func NewObjects() *Objects { return &Objects{M: map[string][]byte{}} }

// NewInbox returns an empty inbox.
func NewInbox() *Inbox {
	return &Inbox{receipts: map[ulid.ID]webhooks.Receipt{}, bodies: map[ulid.ID][]byte{}, outcome: map[ulid.ID]string{}}
}

// Outcome is what processing recorded for a receipt.
func (m *Inbox) Outcome(id ulid.ID) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.outcome[id]
}

// Orders keeps orders in memory and moves them only along the
// edges a carrier makes; moves happen when the caller's tx commits.
type Orders struct {
	mu     sync.Mutex
	orders map[ulid.ID]*app.OrderInfo
	Moves  []string // "<order> <to> <actor>"
	Refuse bool     // refuse every move (the merchant cancelled the order)
}

// Add stores an order in status and returns its ID.
func (o *Orders) Add(t *testing.T, status string) ulid.ID {
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

// Order implements app.Orders.
func (o *Orders) Order(_ context.Context, id ulid.ID) (app.OrderInfo, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	info, ok := o.orders[id]
	if !ok {
		return app.OrderInfo{}, app.ErrOrderUnknown
	}
	return *info, nil
}

// MoveTx implements app.Orders.
func (o *Orders) MoveTx(_ context.Context, _ pgx.Tx, id ulid.ID, to, actor, _ string) (bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.Refuse {
		return false, app.ErrOrderMoveRefused
	}
	info := o.orders[id]
	if info.Status == to {
		return false, nil
	}
	info.Status = to
	o.Moves = append(o.Moves, id.String()[20:]+" "+to+" "+actor)
	return true, nil
}

// Status is an order's current status.
func (o *Orders) Status(id ulid.ID) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.orders[id].Status
}

// Jobs keeps the jobs enqueued in committed transactions.
type Jobs struct {
	mu   sync.Mutex
	Args []river.JobArgs
	Opts []*river.InsertOpts
}

// InsertTx implements jobs.Inserter: it keeps the job.
func (c *Jobs) InsertTx(_ context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	if tx == nil {
		return nil, errors.New("jobs must be enqueued in a transaction")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Args = append(c.Args, args)
	c.Opts = append(c.Opts, opts)
	return &rivertype.JobInsertResult{}, nil
}

// Kinds lists the kinds of the jobs enqueued so far.
func (c *Jobs) Kinds() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.Args))
	for i, j := range c.Args {
		out[i] = j.Kind()
	}
	return out
}

// Objects is an in-memory object store.
type Objects struct {
	mu sync.Mutex
	M  map[string][]byte
}

// PresignUpload implements media.Objects (unused by shipping).
func (o *Objects) PresignUpload(context.Context, string, string, int64, time.Duration) (media.Upload, error) {
	return media.Upload{}, errors.New("not used")
}

// Get implements media.Objects.
func (o *Objects) Get(_ context.Context, key string, _ int64) ([]byte, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	b, ok := o.M[key]
	if !ok {
		return nil, errors.New("no such object")
	}
	return b, nil
}

// Put implements media.Objects.
func (o *Objects) Put(_ context.Context, key, _ string, data []byte) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.M[key] = data
	return nil
}

// Delete implements media.Objects.
func (o *Objects) Delete(_ context.Context, key string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.M, key)
	return nil
}

// Inbox is the webhooks inbox in memory.
type Inbox struct {
	mu       sync.Mutex
	receipts map[ulid.ID]webhooks.Receipt
	bodies   map[ulid.ID][]byte
	outcome  map[ulid.ID]string
}

// Get implements webhooks.Inbox.
func (m *Inbox) Get(_ context.Context, id ulid.ID) (webhooks.Receipt, string, []byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.receipts[id]
	if !ok {
		return webhooks.Receipt{}, "", nil, webhooks.ErrReceiptNotFound
	}
	return r, "application/json", m.bodies[id], nil
}

// MarkProcessedTx implements webhooks.Inbox.
func (m *Inbox) MarkProcessedTx(_ context.Context, _ pgx.Tx, id ulid.ID, outcome string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, done := m.outcome[id]; done {
		return false, nil
	}
	m.outcome[id] = outcome
	return true, nil
}

// Put stores an accepted delivery of provider and returns its receipt ID.
func (m *Inbox) Put(provider string, body []byte) ulid.ID {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := ulid.NewGenerator().New()
	m.receipts[id] = webhooks.Receipt{ID: id, Provider: provider}
	m.bodies[id] = body
	return id
}
