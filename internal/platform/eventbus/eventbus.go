// Package eventbus delivers domain events between modules through the
// transactional outbox (ADR-0003, ADR-0015).
//
// A module publishes an event inside the transaction that changes its
// state. PublishTx inserts one River job per subscriber of the event's
// type in that same transaction, so an event exists exactly when the
// change commits, and each subscriber retries on its own when it fails.
//
// Subscriptions are made once, at startup, in cmd/platform, and every
// role builds the same Bus: the role that publishes needs the subscriber
// list, the worker needs the handlers. There is no global registry.
package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

// Errors.
var (
	ErrInvalidSubscriber = errors.New("eventbus: subscriber names are 1-64 characters of [a-z0-9_.]")
	ErrDuplicate         = errors.New("eventbus: subscriber already subscribed to this event type")
	ErrNoHandler         = errors.New("eventbus: no handler for this subscriber and event type")
	ErrStoreMismatch     = errors.New("eventbus: event store differs from the tenant in context")
)

// Publisher is what modules depend on to emit events.
type Publisher interface {
	// PublishTx enqueues ev for every subscriber of its type in tx.
	PublishTx(ctx context.Context, tx pgx.Tx, ev event.Event) error
}

// Nop discards events. It is for module tests that do not observe them.
type Nop struct{}

// PublishTx implements Publisher.
func (Nop) PublishTx(context.Context, pgx.Tx, event.Event) error { return nil }

// Delivery is one event as a subscriber receives it.
type Delivery struct {
	Meta    event.Meta
	Payload json.RawMessage // the whole event as published
}

// Handler handles a delivery. It runs in a River job with the event's
// store in context, may run more than once, and may see events out of
// order: handlers must be idempotent and read current state when order
// matters.
type Handler func(ctx context.Context, d Delivery) error

// Decode unmarshals a delivery into the event type T.
func Decode[T event.Event](d Delivery) (T, error) {
	var ev T
	if err := json.Unmarshal(d.Payload, &ev); err != nil {
		return ev, fmt.Errorf("eventbus: decode %s: %w", d.Meta.Type, err)
	}
	return ev, nil
}

// Typed adapts a function of one event type to a Handler.
func Typed[T event.Event](fn func(ctx context.Context, ev T) error) Handler {
	return func(ctx context.Context, d Delivery) error {
		ev, err := Decode[T](d)
		if err != nil {
			return river.JobCancel(err) // retrying cannot fix a payload that does not decode
		}
		return fn(ctx, ev)
	}
}

type key struct{ eventType, subscriber string }

// Bus routes events to subscribers. Subscribe before the first PublishTx;
// the Bus is not safe for concurrent Subscribe.
type Bus struct {
	ins      jobs.Inserter
	subs     map[string][]string // event type → subscribers, in subscription order
	handlers map[key]Handler
}

// New returns an empty bus that inserts jobs with ins.
func New(ins jobs.Inserter) *Bus {
	return &Bus{ins: ins, subs: map[string][]string{}, handlers: map[key]Handler{}}
}

// Subscribe registers h as subscriber's handler for eventType. The
// subscriber name is stored in queued jobs: renaming it strands jobs
// queued under the old name until they are retried out.
func (b *Bus) Subscribe(eventType, subscriber string, h Handler) error {
	if err := event.ValidateType(eventType); err != nil {
		return err
	}
	if !validSubscriber(subscriber) {
		return fmt.Errorf("%w: %q", ErrInvalidSubscriber, subscriber)
	}
	if h == nil {
		return fmt.Errorf("eventbus: nil handler for %s/%s", eventType, subscriber)
	}
	k := key{eventType, subscriber}
	if _, ok := b.handlers[k]; ok {
		return fmt.Errorf("%w: %s/%s", ErrDuplicate, eventType, subscriber)
	}
	b.handlers[k] = h
	b.subs[eventType] = append(b.subs[eventType], subscriber)
	return nil
}

func validSubscriber(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' && c != '.' {
			return false
		}
	}
	return true
}

// PublishTx implements Publisher. An event nobody subscribes to costs
// nothing.
func (b *Bus) PublishTx(ctx context.Context, tx pgx.Tx, ev event.Event) error {
	m := ev.EventMeta()
	if err := event.ValidateType(m.Type); err != nil {
		return err
	}
	if m.ID.IsZero() || m.Version < 1 {
		return fmt.Errorf("eventbus: %s: event has no ID or version (build Meta with event.NewMeta)", m.Type)
	}
	cur, err := tenant.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("eventbus: %w", err)
	}
	if m.StoreID != cur {
		return fmt.Errorf("%w (%s)", ErrStoreMismatch, m.Type)
	}
	subs := b.subs[m.Type]
	if len(subs) == 0 {
		return nil
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("eventbus: encode %s: %w", m.Type, err)
	}
	for _, s := range subs {
		args := DeliverArgs{TenantArgs: jobs.TenantArgs{StoreID: m.StoreID}, Subscriber: s, Type: m.Type, Event: payload}
		if _, err = b.ins.InsertTx(ctx, tx, args, nil); err != nil {
			return fmt.Errorf("eventbus: enqueue %s for %s: %w", m.Type, s, err)
		}
	}
	return nil
}

// DeliverArgs is one event for one subscriber.
type DeliverArgs struct {
	jobs.TenantArgs
	Subscriber string          `json:"subscriber"`
	Type       string          `json:"type"`
	Event      json.RawMessage `json:"event"`
}

// Kind implements river.JobArgs.
func (DeliverArgs) Kind() string { return "event.deliver" }

// Worker runs the bus's deliveries. Register it on the worker role with
// river.AddWorkerSafely.
func (b *Bus) Worker() river.Worker[DeliverArgs] { return &worker{bus: b} }

type worker struct {
	river.WorkerDefaults[DeliverArgs]
	bus *Bus
}

// Work decodes the envelope and calls the subscriber's handler. A missing
// handler is retried: during a rolling deploy the job may reach a worker
// that does not have the subscription yet.
func (w *worker) Work(ctx context.Context, job *river.Job[DeliverArgs]) error {
	a := job.Args
	h, ok := w.bus.handlers[key{a.Type, a.Subscriber}]
	if !ok {
		return fmt.Errorf("%w: %s/%s", ErrNoHandler, a.Type, a.Subscriber)
	}
	var m event.Meta
	if err := json.Unmarshal(a.Event, &m); err != nil {
		return river.JobCancel(fmt.Errorf("eventbus: decode envelope of %s: %w", a.Type, err))
	}
	if m.Type != a.Type || m.StoreID != a.StoreID {
		return river.JobCancel(fmt.Errorf("eventbus: envelope of %s does not match its job", a.Type))
	}
	return h(ctx, Delivery{Meta: m, Payload: a.Event})
}
