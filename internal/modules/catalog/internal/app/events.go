package app

import (
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Events publishes the catalog's domain events in the caller's
// transaction (ADR-0015). The zero value discards them, for workers and
// tests that do not need them.
type Events struct {
	Pub eventbus.Publisher
	IDs *ulid.Generator
}

func (e Events) publish(ctx context.Context, tx pgx.Tx, typ string, mk func(event.Meta) event.Event) error {
	if e.Pub == nil {
		return nil
	}
	m, err := event.MetaFor(ctx, e.IDs, typ, 1)
	if err != nil {
		return fmt.Errorf("catalog: publish %s: %w", typ, err)
	}
	return e.Pub.PublishTx(ctx, tx, mk(m))
}

func (e Events) productChanged(ctx context.Context, tx pgx.Tx, id ulid.ID, reason string) error {
	return e.publish(ctx, tx, domain.EventProductChanged, func(m event.Meta) event.Event {
		return domain.ProductChanged{Meta: m, ProductID: id, Reason: reason}
	})
}

func (e Events) productDeleted(ctx context.Context, tx pgx.Tx, id ulid.ID) error {
	return e.publish(ctx, tx, domain.EventProductDeleted, func(m event.Meta) event.Event {
		return domain.ProductDeleted{Meta: m, ProductID: id}
	})
}

func (e Events) categoryChanged(ctx context.Context, tx pgx.Tx, id ulid.ID, reason string) error {
	return e.publish(ctx, tx, domain.EventCategoryChanged, func(m event.Meta) event.Event {
		return domain.CategoryChanged{Meta: m, CategoryID: id, Reason: reason}
	})
}

func (e Events) sizeChartChanged(ctx context.Context, tx pgx.Tx, id ulid.ID, reason string) error {
	return e.publish(ctx, tx, domain.EventSizeChartChanged, func(m event.Meta) event.Event {
		return domain.SizeChartChanged{Meta: m, SizeChartID: id, Reason: reason}
	})
}

// stockChanged publishes one ReasonStock change per distinct product.
func (e Events) stockChanged(ctx context.Context, tx pgx.Tx, productIDs []ulid.ID) error {
	seen := make([]ulid.ID, 0, len(productIDs))
	for _, id := range productIDs {
		if slices.Contains(seen, id) {
			continue
		}
		seen = append(seen, id)
		if err := e.productChanged(ctx, tx, id, domain.ReasonStock); err != nil {
			return err
		}
	}
	return nil
}
