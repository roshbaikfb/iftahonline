package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/store/storefrontdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// ErrTooManyPreviews means the store already has domain.MaxPreviews live
// preview links.
var ErrTooManyPreviews = errors.New("storefront store: too many preview links")

// GetDraft returns the store's drafts (empty when there are none).
func GetDraft(ctx context.Context, tx Tx) (domain.Draft, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Draft{}, err
	}
	row, err := storefrontdb.New(tx).GetDraft(ctx, store)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Draft{}, nil
	}
	if err != nil {
		return domain.Draft{}, fmt.Errorf("storefront store: get draft: %w", err)
	}
	d, err := toDraft(row.Theme, row.Home)
	if err != nil {
		return domain.Draft{}, err
	}
	d.UpdatedAt = &row.UpdatedAt
	return d, nil
}

// SaveThemeDraft replaces the theme draft.
func SaveThemeDraft(ctx context.Context, tx Tx, s domain.ThemeSettings) (domain.Draft, error) {
	return saveDraft(ctx, tx, "theme", s, func(q *storefrontdb.Queries, store ulid.ID, b []byte) (storefrontdb.StorefrontDraft, error) {
		return q.SaveThemeDraft(ctx, storefrontdb.SaveThemeDraftParams{StoreID: store, Theme: b})
	})
}

// SaveHomeDraft replaces the home page draft.
func SaveHomeDraft(ctx context.Context, tx Tx, h domain.HomeDraft) (domain.Draft, error) {
	return saveDraft(ctx, tx, "home", h, func(q *storefrontdb.Queries, store ulid.ID, b []byte) (storefrontdb.StorefrontDraft, error) {
		return q.SaveHomeDraft(ctx, storefrontdb.SaveHomeDraftParams{StoreID: store, Home: b})
	})
}

// saveDraft encodes one part of the drafts and stores it with save.
func saveDraft(ctx context.Context, tx Tx, part string, v any,
	save func(*storefrontdb.Queries, ulid.ID, []byte) (storefrontdb.StorefrontDraft, error)) (domain.Draft, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Draft{}, err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return domain.Draft{}, fmt.Errorf("storefront store: encode %s draft: %w", part, err)
	}
	row, err := save(storefrontdb.New(tx), store, b)
	if err != nil {
		return domain.Draft{}, fmt.Errorf("storefront store: save %s draft: %w", part, err)
	}
	return draftRow(row)
}

// ClearThemeDraft drops the theme draft.
func ClearThemeDraft(ctx context.Context, tx Tx) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = storefrontdb.New(tx).ClearThemeDraft(ctx, store); err != nil {
		return fmt.Errorf("storefront store: clear theme draft: %w", err)
	}
	return nil
}

// ClearHomeDraft drops the home page draft.
func ClearHomeDraft(ctx context.Context, tx Tx) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = storefrontdb.New(tx).ClearHomeDraft(ctx, store); err != nil {
		return fmt.Errorf("storefront store: clear home draft: %w", err)
	}
	return nil
}

// InsertPreview records a preview link by its token's hash, after the
// store's expired links go; at most domain.MaxPreviews live at once.
func InsertPreview(ctx context.Context, tx Tx, hash []byte, expires time.Time) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	q := storefrontdb.New(tx)
	if err = q.DeleteExpiredPreviews(ctx, store); err != nil {
		return fmt.Errorf("storefront store: expire previews: %w", err)
	}
	n, err := q.CountPreviews(ctx, store)
	if err != nil {
		return fmt.Errorf("storefront store: count previews: %w", err)
	}
	if n >= domain.MaxPreviews {
		return ErrTooManyPreviews
	}
	if err = q.InsertPreview(ctx, storefrontdb.InsertPreviewParams{TokenHash: hash, StoreID: store, ExpiresAt: expires}); err != nil {
		return fmt.Errorf("storefront store: insert preview: %w", err)
	}
	return nil
}

// PreviewDraft returns the store's drafts when hash is one of its live
// preview links, or domain.ErrNotFound.
func PreviewDraft(ctx context.Context, tx Tx, hash []byte) (domain.Draft, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Draft{}, err
	}
	row, err := storefrontdb.New(tx).PreviewDraft(ctx, storefrontdb.PreviewDraftParams{StoreID: store, TokenHash: hash})
	if err != nil {
		return domain.Draft{}, notFound(err, "preview draft")
	}
	return toDraft(row.Theme, row.Home)
}

func draftRow(r storefrontdb.StorefrontDraft) (domain.Draft, error) {
	d, err := toDraft(r.Theme, r.Home)
	if err != nil {
		return domain.Draft{}, err
	}
	d.UpdatedAt = &r.UpdatedAt
	return d, nil
}

func toDraft(theme, home []byte) (domain.Draft, error) {
	var d domain.Draft
	if theme != nil {
		var t domain.ThemeSettings
		if err := json.Unmarshal(theme, &t); err != nil {
			return domain.Draft{}, fmt.Errorf("storefront store: decode theme draft: %w", err)
		}
		d.Theme = &t
	}
	if home != nil {
		var h domain.HomeDraft
		if err := json.Unmarshal(home, &h); err != nil {
			return domain.Draft{}, fmt.Errorf("storefront store: decode home draft: %w", err)
		}
		d.Home = &h
	}
	return d, nil
}
