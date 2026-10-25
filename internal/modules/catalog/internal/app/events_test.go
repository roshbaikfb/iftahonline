package app_test

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// eventLog is an eventbus.Publisher that records what was published, as
// "type id reason" strings, after checking the envelope.
type eventLog struct {
	mu  sync.Mutex
	got []string
}

func (l *eventLog) PublishTx(ctx context.Context, tx pgx.Tx, ev event.Event) error {
	m := ev.EventMeta()
	store, err := tenant.FromContext(ctx)
	if err != nil {
		return err
	}
	if tx == nil || m.ID.IsZero() || m.Version != 1 || m.StoreID != store || event.ValidateType(m.Type) != nil {
		return fmt.Errorf("bad publish: tx %v, meta %+v", tx != nil, m)
	}
	var line string
	switch e := ev.(type) {
	case domain.ProductChanged:
		line = fmt.Sprintf("%s %s %s", m.Type, e.ProductID, e.Reason)
	case domain.ProductDeleted:
		line = fmt.Sprintf("%s %s", m.Type, e.ProductID)
	case domain.CategoryChanged:
		line = fmt.Sprintf("%s %s %s", m.Type, e.CategoryID, e.Reason)
	default:
		return fmt.Errorf("unexpected event %T", ev)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.got = append(l.got, line)
	return nil
}

// take returns and clears what was published.
func (l *eventLog) take() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.got
	l.got = nil
	return out
}

func changed(id ulid.ID, reason string) string {
	return fmt.Sprintf("%s %s %s", domain.EventProductChanged, id, reason)
}

func wantEvents(t *testing.T, f fixture, want ...string) {
	t.Helper()
	got := f.events.take()
	if !slices.Equal(got, want) {
		t.Fatalf("events:\n got  %q\n want %q", got, want)
	}
}

func TestProductAndCategoryEvents(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.ctxA

	cat, err := f.svc.CreateCategory(ctx, domain.CategoryInput{Name: "Shoes"})
	if err != nil {
		t.Fatal(err)
	}
	wantEvents(t, f, fmt.Sprintf("%s %s created", domain.EventCategoryChanged, cat.ID))
	if _, err = f.svc.UpdateCategory(ctx, cat.ID, domain.CategoryInput{Name: "Boots"}); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, f, fmt.Sprintf("%s %s updated", domain.EventCategoryChanged, cat.ID))

	p := mustCreate(ctx, t, f, tee("Shirt", 5))
	wantEvents(t, f, changed(p.ID, domain.ReasonCreated))

	in := tee("Shirt", 5)
	in.Variants[0].ID, in.Variants[1].ID = &p.Variants[0].ID, &p.Variants[1].ID
	in.Variants[0].PriceMinor = 9900
	if _, err = f.svc.UpdateProduct(ctx, p.ID, in); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, f, changed(p.ID, domain.ReasonUpdated))

	if _, err = f.svc.SetStock(ctx, p.Variants[0].ID, domain.StockInput{Tracked: true, OnHand: 2}); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, f, changed(p.ID, domain.ReasonStock))

	// A failed write publishes nothing (the event is in the rolled-back
	// transaction, and here never even reached).
	if _, err = f.svc.SetStock(ctx, ulid.NewGenerator().New(), domain.StockInput{Tracked: true, OnHand: 1}); err == nil {
		t.Fatal("unknown variant accepted")
	}
	wantEvents(t, f)

	if err = f.svc.DeleteProduct(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, f, fmt.Sprintf("%s %s", domain.EventProductDeleted, p.ID))

	if err = f.svc.DeleteCategory(ctx, cat.ID); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, f, fmt.Sprintf("%s %s deleted", domain.EventCategoryChanged, cat.ID))
}

// TestStockTransitionEvents: reservations publish only when a variant
// sells out or comes back, never for every unit.
func TestStockTransitionEvents(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.ctxA
	p := mustCreate(ctx, t, f, tee("Cap", 2))
	untracked := mustCreate(ctx, t, f, single("Gift card", 0, false))
	plenty := mustCreate(ctx, t, f, single("Socks", domain.MaxShownLeft+5, true))
	f.events.take()
	v := p.Variants[0]
	line := func(id ulid.ID, qty int) []domain.ReserveLine { return []domain.ReserveLine{{VariantID: id, Qty: qty}} }

	// Plenty left: nothing a shopper sees changes, so no event (and no
	// purge of cached pages on every order).
	if _, err := f.svc.ReserveStock(ctx, domain.ReserveRequest{Ref: "p", Lines: line(plenty.Variants[0].ID, 4)}); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, f)
	// Down to "only 10 left": shoppers now see a count.
	if _, err := f.svc.ReserveStock(ctx, domain.ReserveRequest{Ref: "p2", Lines: line(plenty.Variants[0].ID, 1)}); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, f, changed(plenty.ID, domain.ReasonStock))

	if _, err := f.svc.ReserveStock(ctx, domain.ReserveRequest{Ref: "a", Lines: line(v.ID, 1)}); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, f, changed(p.ID, domain.ReasonStock)) // "only 2 left" became "only 1 left"
	// Both variants of one product sell out in one reservation: one event.
	if _, err := f.svc.ReserveStock(ctx, domain.ReserveRequest{Ref: "b", Lines: []domain.ReserveLine{
		{VariantID: v.ID, Qty: 1}, {VariantID: p.Variants[1].ID, Qty: 2},
	}}); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, f, changed(p.ID, domain.ReasonStock))
	if _, err := f.svc.ReserveStock(ctx, domain.ReserveRequest{Ref: "u", Lines: line(untracked.Variants[0].ID, 3)}); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, f) // untracked stock never sells out

	if err := f.svc.ReleaseStock(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, f, changed(p.ID, domain.ReasonStock)) // back in stock
	if err := f.svc.ReleaseStock(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, f) // idempotent release: nothing changed

	// Expiry brings stock back the same way.
	f.svc.SetClock(func() time.Time { return time.Now().Add(-time.Hour) })
	if _, err := f.svc.ReserveStock(ctx, domain.ReserveRequest{Ref: "old", Lines: line(v.ID, 1)}); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, f, changed(p.ID, domain.ReasonStock))
	worker := &app.ExpireWorker{DB: f.pool, Events: app.Events{Pub: f.events, IDs: f.ids}}
	args := app.ExpireReservationArgs{TenantArgs: jobs.TenantArgs{StoreID: mustStore(ctx, t)}, Ref: "old"}
	if err := worker.Work(ctx, &river.Job[app.ExpireReservationArgs]{Args: args}); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, f, changed(p.ID, domain.ReasonStock))
}

func TestImageEvents(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.ctxA
	p := mustCreate(ctx, t, f, single("Lamp", 1, true))
	img, _, err := f.svc.CreateImageUpload(ctx, p.ID, "image/jpeg", domain.ImageInput{})
	if err != nil {
		t.Fatal(err)
	}
	f.events.take()
	if err = f.objects.Put(ctx, img.UploadKey, "image/jpeg", photo(t, 40, 30)); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.CompleteImageUpload(ctx, p.ID, img.ID); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, f) // not visible until processed
	runJobs(ctx, t, f)
	wantEvents(t, f, changed(p.ID, domain.ReasonImages))

	if _, err = f.svc.UpdateImage(ctx, p.ID, img.ID, domain.ImageInput{Alt: "lamp"}); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, f, changed(p.ID, domain.ReasonImages))
	if err = f.svc.DeleteImage(ctx, p.ID, img.ID); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, f, changed(p.ID, domain.ReasonImages))
}

func TestEventsZeroValueDiscards(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	deps := f.deps
	deps.Events = nil
	svc := app.NewService(deps)
	if _, err := svc.CreateProduct(f.ctxA, single("Quiet", 1, true)); err != nil {
		t.Fatal(err)
	}
	wantEvents(t, f)
}
