package testkit

import (
	"context"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

// NewEvents returns an empty event capture, an eventbus.Publisher for
// module tests.
func NewEvents() *Events { return &Events{} }

// Events keeps what was published, after checking each envelope: in a
// transaction, with an ID, a version, a valid type and the store in ctx.
type Events struct {
	mu  sync.Mutex
	got []event.Event
}

// PublishTx implements eventbus.Publisher.
func (e *Events) PublishTx(ctx context.Context, tx pgx.Tx, ev event.Event) error {
	m := ev.EventMeta()
	store, err := tenant.FromContext(ctx)
	if err != nil {
		return err
	}
	if tx == nil || m.ID.IsZero() || m.Version < 1 || m.StoreID != store || event.ValidateType(m.Type) != nil {
		return fmt.Errorf("testkit: bad publish: in a tx %v, meta %+v", tx != nil, m)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.got = append(e.got, ev)
	return nil
}

// All returns the events published so far.
func (e *Events) All() []event.Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]event.Event(nil), e.got...)
}
