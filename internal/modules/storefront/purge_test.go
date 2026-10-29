package storefront_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/marketing"
	"github.com/roshbaikfb/iftahonline/internal/modules/payments"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront"
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

type recordingPurger struct {
	tags []string
	err  error
}

func (r *recordingPurger) PurgeTags(_ context.Context, tags []string) error {
	r.tags = append(r.tags, tags...)
	return r.err
}

func delivery(t *testing.T, store tenant.StoreID, typ string, mk func(event.Meta) event.Event) eventbus.Delivery {
	t.Helper()
	m, err := event.NewMeta(ulid.NewGenerator(), time.Now(), store, typ, 1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(mk(m))
	if err != nil {
		t.Fatal(err)
	}
	return eventbus.Delivery{Meta: m, Payload: b}
}

func TestPurgeTags(t *testing.T) {
	t.Parallel()
	gen := ulid.NewGenerator()
	store := tenant.StoreID{ID: gen.New()}
	s := store.String()
	prod, page := gen.New(), gen.New()
	tests := []struct {
		name string
		d    eventbus.Delivery
		want []string
	}{
		{"product changed", delivery(t, store, catalog.EventProductChanged, func(m event.Meta) event.Event {
			return catalog.ProductChanged{Meta: m, ProductID: prod, Reason: "updated"}
		}), []string{"p-" + prod.String(), "l-" + s}},
		{"product deleted", delivery(t, store, catalog.EventProductDeleted, func(m event.Meta) event.Event {
			return catalog.ProductDeleted{Meta: m, ProductID: prod}
		}), []string{"p-" + prod.String(), "l-" + s}},
		{"category", delivery(t, store, catalog.EventCategoryChanged, func(m event.Meta) event.Event {
			return catalog.CategoryChanged{Meta: m, CategoryID: gen.New(), Reason: "updated"}
		}), []string{"s-" + s}},
		{"size chart", delivery(t, store, catalog.EventSizeChartChanged, func(m event.Meta) event.Event {
			return catalog.SizeChartChanged{Meta: m, SizeChartID: gen.New(), Reason: "updated"}
		}), []string{"s-" + s}}, // the event does not list the chart's products
		{"theme", delivery(t, store, storefront.EventThemeChanged, func(m event.Meta) event.Event {
			return storefront.ThemeChanged{Meta: m}
		}), []string{"s-" + s}},
		{"landing page", delivery(t, store, storefront.EventPageChanged, func(m event.Meta) event.Event {
			return storefront.PageChanged{Meta: m, PageID: page, Kind: storefront.KindLanding, Slug: "offer"}
		}), []string{"pg-" + page.String()}},
		{"policy", delivery(t, store, storefront.EventPageChanged, func(m event.Meta) event.Event {
			return storefront.PageChanged{Meta: m, PageID: page, Kind: storefront.KindPolicy, Slug: "refund"}
		}), []string{"s-" + s}},
		{"gateway connected", delivery(t, store, payments.EventConnectionChanged, func(m event.Meta) event.Event {
			return payments.ConnectionChanged{Meta: m}
		}), []string{"s-" + s}}, // every footer lists the payment methods
		{"pixels changed", delivery(t, store, marketing.EventPixelsChanged, func(m event.Meta) event.Event {
			return marketing.PixelsChanged{Meta: m, Provider: marketing.Meta}
		}), []string{"s-" + s}}, // every page loads the store's tags
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := storefront.PurgeTags(tt.d)
			if err != nil || !slices.Equal(got, tt.want) {
				t.Fatalf("tags %q, %v; want %q", got, err, tt.want)
			}
			p := &recordingPurger{}
			if err = storefront.PurgeHandler(p)(t.Context(), tt.d); err != nil || !slices.Equal(p.tags, tt.want) {
				t.Fatalf("handler purged %q, %v", p.tags, err)
			}
		})
	}

	// Every invalidating event has a mapping, and a purge failure is
	// retried (returned), while a broken payload is not.
	for _, typ := range storefront.InvalidatingEvents() {
		if _, err := storefront.PurgeTags(eventbus.Delivery{Meta: event.Meta{Type: typ, StoreID: store}, Payload: []byte(`{}`)}); err != nil {
			t.Errorf("%s: %v", typ, err)
		}
	}
	boom := errors.New("cloudflare down")
	if err := storefront.PurgeHandler(&recordingPurger{err: boom})(t.Context(), tests[0].d); !errors.Is(err, boom) {
		t.Fatalf("purge failure: %v", err)
	}
	bad := eventbus.Delivery{Meta: event.Meta{Type: catalog.EventProductChanged, StoreID: store}, Payload: []byte(`{"product_id":7}`)}
	var cancel *river.JobCancelError
	if err := storefront.PurgeHandler(&recordingPurger{})(t.Context(), bad); !errors.As(err, &cancel) {
		t.Fatalf("broken payload: %v", err)
	}
}
