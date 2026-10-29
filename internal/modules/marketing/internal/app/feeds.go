package app

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
)

// FeedKey returns the store's product feed key (ADR-0044); ok is false
// when its feeds are off.
func (s *Service) FeedKey(ctx context.Context) (key string, ok bool, err error) {
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (err error) {
		key, ok, err = store.FeedKey(ctx, tx)
		return err
	})
	return key, ok, err
}

// RenewFeedKey turns the store's feeds on with a new key, or replaces
// their key: the old URLs stop working.
func (s *Service) RenewFeedKey(ctx context.Context) (string, error) {
	key, err := domain.NewFeedKey()
	if err != nil {
		return "", err
	}
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if txErr := store.SetFeedKey(ctx, tx, key); txErr != nil {
			return txErr
		}
		if txErr := s.feedChanged(ctx, tx); txErr != nil {
			return txErr
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "product_feed.key_renewed", TargetType: "product_feed"})
	})
	return key, err
}

// DisableFeeds turns the store's feeds off; turning them off again does
// nothing.
func (s *Service) DisableFeeds(ctx context.Context) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		changed, err := store.DeleteFeed(ctx, tx)
		if err != nil || !changed {
			return err
		}
		if err = s.feedChanged(ctx, tx); err != nil {
			return err
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "product_feed.disabled", TargetType: "product_feed"})
	})
}

// FeedAllowed reports whether key opens the store's feeds: they are on
// and it is their key. The storefront asks before it serves one.
func (s *Service) FeedAllowed(ctx context.Context, key string) (bool, error) {
	if len(key) != domain.FeedKeyLen {
		return false, nil // no query for a key that cannot match
	}
	want, ok, err := s.FeedKey(ctx)
	if err != nil || !ok {
		return false, err
	}
	return domain.SameKey(key, want), nil
}

func (s *Service) feedChanged(ctx context.Context, tx pgx.Tx) error {
	meta, err := event.MetaFor(ctx, s.ids, domain.EventFeedChanged, 1)
	if err != nil {
		return err
	}
	if err = s.events.PublishTx(ctx, tx, domain.FeedChanged{Meta: meta}); err != nil {
		return fmt.Errorf("marketing: publish: %w", err)
	}
	return nil
}
