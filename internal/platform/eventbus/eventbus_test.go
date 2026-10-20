package eventbus_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"

	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

type priceChanged struct {
	event.Meta
	ProductID ulid.ID `json:"product_id"`
}

var gen = ulid.NewGenerator()

func newStore() tenant.StoreID { return tenant.StoreID{ID: gen.New()} }

func newEvent(t *testing.T, store tenant.StoreID, typ string) priceChanged {
	t.Helper()
	m, err := event.NewMeta(gen, time.Now(), store, typ, 1)
	if err != nil {
		t.Fatal(err)
	}
	return priceChanged{Meta: m, ProductID: gen.New()}
}

// fakeInserter records inserted args and checks the jobs middleware's
// tenant rule.
type fakeInserter struct {
	args []river.JobArgs
	err  error
}

func (f *fakeInserter) InsertTx(_ context.Context, _ pgx.Tx, args river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.args = append(f.args, args)
	return &rivertype.JobInsertResult{}, nil
}

func nopHandler(context.Context, eventbus.Delivery) error { return nil }

func TestSubscribe(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		typ, sub   string
		h          eventbus.Handler
		wantErr    error
		wantAnyErr bool
	}{
		{"ok", "catalog.product.changed", "storefront.purge", nopHandler, nil, false},
		{"bad type", "catalog.changed", "storefront.purge", nopHandler, event.ErrInvalidType, false},
		{"empty subscriber", "catalog.product.changed", "", nopHandler, eventbus.ErrInvalidSubscriber, false},
		{"upper subscriber", "catalog.product.changed", "Storefront", nopHandler, eventbus.ErrInvalidSubscriber, false},
		{"long subscriber", "catalog.product.changed", string(make([]byte, 65)), nopHandler, eventbus.ErrInvalidSubscriber, false},
		{"nil handler", "catalog.product.changed", "storefront.purge", nil, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := eventbus.New(&fakeInserter{}).Subscribe(tt.typ, tt.sub, tt.h)
			if tt.wantAnyErr {
				if err == nil {
					t.Fatal("want an error")
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}

	b := eventbus.New(&fakeInserter{})
	if err := b.Subscribe("catalog.product.changed", "a", nopHandler); err != nil {
		t.Fatal(err)
	}
	if err := b.Subscribe("catalog.product.changed", "a", nopHandler); !errors.Is(err, eventbus.ErrDuplicate) {
		t.Fatalf("duplicate: err = %v", err)
	}
	if err := b.Subscribe("catalog.product.deleted", "a", nopHandler); err != nil {
		t.Fatalf("same subscriber, other type: %v", err)
	}
}

func TestPublishTx(t *testing.T) {
	t.Parallel()
	store := newStore()
	ctx := tenant.WithStore(t.Context(), store)

	ins := &fakeInserter{}
	b := eventbus.New(ins)
	for _, s := range []string{"storefront.purge", "storefront.cache"} {
		if err := b.Subscribe("catalog.product.changed", s, nopHandler); err != nil {
			t.Fatal(err)
		}
	}
	ev := newEvent(t, store, "catalog.product.changed")
	if err := b.PublishTx(ctx, nil, ev); err != nil {
		t.Fatal(err)
	}
	if len(ins.args) != 2 {
		t.Fatalf("inserted %d jobs, want one per subscriber (2)", len(ins.args))
	}
	for i, want := range []string{"storefront.purge", "storefront.cache"} {
		a, ok := ins.args[i].(eventbus.DeliverArgs)
		if !ok {
			t.Fatalf("args %T", ins.args[i])
		}
		var got priceChanged
		if err := json.Unmarshal(a.Event, &got); err != nil {
			t.Fatal(err)
		}
		if a.Subscriber != want || a.Type != ev.Type || a.StoreID != store || got != ev {
			t.Fatalf("args[%d] = %+v (event %+v)", i, a, got)
		}
	}

	// Nobody subscribes: nothing is inserted.
	ins.args = nil
	if err := b.PublishTx(ctx, nil, newEvent(t, store, "catalog.category.changed")); err != nil || len(ins.args) != 0 {
		t.Fatalf("unsubscribed type: err %v, %d jobs", err, len(ins.args))
	}

	tests := []struct {
		name    string
		ctx     context.Context
		ev      event.Event
		wantErr error
	}{
		{"other store", ctx, newEvent(t, newStore(), "catalog.product.changed"), eventbus.ErrStoreMismatch},
		{"no tenant", t.Context(), ev, tenant.ErrNoTenant},
		{"bad type", ctx, priceChanged{Meta: event.Meta{ID: gen.New(), Type: "x", Version: 1, StoreID: store}}, event.ErrInvalidType},
		{"zero meta", ctx, priceChanged{Meta: event.Meta{Type: "catalog.product.changed", StoreID: store}}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := eventbus.New(&fakeInserter{}).PublishTx(tt.ctx, nil, tt.ev)
			if err == nil || (tt.wantErr != nil && !errors.Is(err, tt.wantErr)) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}

	boom := errors.New("boom")
	failing := eventbus.New(&fakeInserter{err: boom})
	if err := failing.Subscribe("catalog.product.changed", "s", nopHandler); err != nil {
		t.Fatal(err)
	}
	if err := failing.PublishTx(ctx, nil, ev); !errors.Is(err, boom) {
		t.Fatalf("insert failure: err = %v", err)
	}
}

func TestNop(t *testing.T) {
	t.Parallel()
	if err := (eventbus.Nop{}).PublishTx(t.Context(), nil, priceChanged{}); err != nil {
		t.Fatal(err)
	}
}

func deliverJob(t *testing.T, a eventbus.DeliverArgs) *river.Job[eventbus.DeliverArgs] {
	t.Helper()
	return &river.Job[eventbus.DeliverArgs]{JobRow: &rivertype.JobRow{ID: 1, Kind: a.Kind()}, Args: a}
}

func TestWorker(t *testing.T) {
	t.Parallel()
	store := newStore()
	ev := newEvent(t, store, "catalog.product.changed")
	payload, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var got priceChanged
	b := eventbus.New(&fakeInserter{})
	err = b.Subscribe("catalog.product.changed", "s", eventbus.Typed(func(_ context.Context, e priceChanged) error {
		got = e
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	w := b.Worker()
	ok := eventbus.DeliverArgs{TenantArgs: jobs.TenantArgs{StoreID: store}, Subscriber: "s", Type: ev.Type, Event: payload}
	if err = w.Work(t.Context(), deliverJob(t, ok)); err != nil || got != ev {
		t.Fatalf("work: err %v, handler saw %+v", err, got)
	}

	tests := []struct {
		name       string
		mutate     func(a *eventbus.DeliverArgs)
		wantCancel bool
		wantErr    error
	}{
		{"unknown subscriber retries", func(a *eventbus.DeliverArgs) { a.Subscriber = "gone" }, false, eventbus.ErrNoHandler},
		{"bad envelope", func(a *eventbus.DeliverArgs) { a.Event = json.RawMessage(`[`) }, true, nil},
		{"envelope type differs", func(a *eventbus.DeliverArgs) {
			a.Event = json.RawMessage(`{"type":"catalog.product.deleted","store_id":"` + store.String() + `"}`)
		}, true, nil},
		{"envelope store differs", func(a *eventbus.DeliverArgs) { a.StoreID = newStore() }, true, nil},
		{"payload does not decode", func(a *eventbus.DeliverArgs) {
			a.Event = json.RawMessage(`{"type":"catalog.product.changed","store_id":"` + store.String() + `","product_id":7}`)
		}, true, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := ok
			tt.mutate(&a)
			err := w.Work(t.Context(), deliverJob(t, a))
			var cancel *river.JobCancelError
			if tt.wantCancel != errors.As(err, &cancel) {
				t.Fatalf("err = %v, want cancel %v", err, tt.wantCancel)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

type seen struct {
	tenant tenant.StoreID
	ev     priceChanged
}

// TestDeliveryOnRealRiver publishes in a real transaction on PostgreSQL:
// a committed event reaches its subscriber with the store in context; a
// rolled-back one never does.
func TestDeliveryOnRealRiver(t *testing.T) {
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	inserter, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Middleware: jobs.Middleware()})
	if err != nil {
		t.Fatal(err)
	}
	bus := eventbus.New(inserter)
	got := make(chan seen, 4)
	err = bus.Subscribe("test.price.changed", "test.sub", eventbus.Typed(func(ctx context.Context, e priceChanged) error {
		id, tErr := tenant.FromContext(ctx)
		if tErr != nil {
			return tErr
		}
		got <- seen{tenant: id, ev: e}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	if err = river.AddWorkerSafely(workers, bus.Worker()); err != nil {
		t.Fatal(err)
	}
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Queues:     map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 2}},
		Workers:    workers,
		Middleware: jobs.Middleware(),
		TestOnly:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = client.Stop(stopCtx)
	})

	store := newStore()
	inStore := tenant.WithStore(ctx, store)
	rolledBack := newEvent(t, store, "test.price.changed")
	errRollback := errors.New("rollback")
	err = pgx.BeginFunc(inStore, pool, func(tx pgx.Tx) error {
		if pErr := bus.PublishTx(inStore, tx, rolledBack); pErr != nil {
			return pErr
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatal(err)
	}
	committed := newEvent(t, store, "test.price.changed")
	if err = pgx.BeginFunc(inStore, pool, func(tx pgx.Tx) error { return bus.PublishTx(inStore, tx, committed) }); err != nil {
		t.Fatal(err)
	}

	select {
	case s := <-got:
		if s.tenant != store || s.ev != committed {
			t.Fatalf("delivered %+v, want %+v in store %v", s, committed, store)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("event was not delivered")
	}
	select {
	case s := <-got:
		t.Fatalf("a second delivery arrived (rolled-back event?): %+v", s)
	case <-time.After(500 * time.Millisecond):
	}
}
