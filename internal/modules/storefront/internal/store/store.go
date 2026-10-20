// Package store adapts the sqlc queries in storefrontdb to domain types.
// Every function runs on a transaction opened by postgres.InTenantTx and
// takes the store ID from tenant.FromContext, never from its arguments
// (CLAUDE.md rule 5); RLS checks it a second time.
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
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Tx is the tenant transaction the functions run on.
type Tx = storefrontdb.DBTX

// ErrSlugTaken means another landing page already uses the slug.
var ErrSlugTaken = errs.Validation(errs.FieldError{Field: "slug", Code: "taken", Message: "another page already uses this slug"})

func current(ctx context.Context) (ulid.ID, error) {
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return ulid.ID{}, fmt.Errorf("storefront store: %w", err)
	}
	return id.ID, nil
}

// GetTheme returns the saved theme, or domain.ErrNotFound.
func GetTheme(ctx context.Context, tx Tx) (domain.Theme, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Theme{}, err
	}
	row, err := storefrontdb.New(tx).GetTheme(ctx, store)
	if err != nil {
		return domain.Theme{}, notFound(err, "get theme")
	}
	return toTheme(row.Settings, row.Version, row.UpdatedAt)
}

// SaveTheme creates the theme (version 0) or updates the version the
// caller read. A lost race is domain.ErrVersionConflict.
func SaveTheme(ctx context.Context, tx Tx, s domain.ThemeSettings, version int) (domain.Theme, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Theme{}, err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return domain.Theme{}, fmt.Errorf("storefront store: encode theme: %w", err)
	}
	q := storefrontdb.New(tx)
	var (
		settings []byte
		ver      int32
		at       time.Time
	)
	if version == 0 {
		var row storefrontdb.InsertThemeRow
		row, err = q.InsertTheme(ctx, storefrontdb.InsertThemeParams{StoreID: store, Settings: b})
		settings, ver, at = row.Settings, row.Version, row.UpdatedAt
	} else {
		var row storefrontdb.UpdateThemeRow
		row, err = q.UpdateTheme(ctx, storefrontdb.UpdateThemeParams{StoreID: store, Settings: b, Version: int32(version)}) //nolint:gosec // a row's version, far below 2^31
		settings, ver, at = row.Settings, row.Version, row.UpdatedAt
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Theme{}, domain.ErrVersionConflict
	}
	if err != nil {
		return domain.Theme{}, fmt.Errorf("storefront store: save theme: %w", err)
	}
	return toTheme(settings, ver, at)
}

func toTheme(settings []byte, version int32, at time.Time) (domain.Theme, error) {
	var s domain.ThemeSettings
	if err := json.Unmarshal(settings, &s); err != nil {
		return domain.Theme{}, fmt.Errorf("storefront store: decode theme: %w", err)
	}
	return domain.Theme{Settings: s, Version: int(version), UpdatedAt: &at}, nil
}

// GetPageByKey returns the page of kind at slug, or domain.ErrNotFound.
func GetPageByKey(ctx context.Context, tx Tx, kind domain.PageKind, slug string) (domain.Page, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Page{}, err
	}
	row, err := storefrontdb.New(tx).GetPageByKey(ctx, storefrontdb.GetPageByKeyParams{StoreID: store, Kind: string(kind), Slug: slug})
	if err != nil {
		return domain.Page{}, notFound(err, "get page")
	}
	return toPage(row)
}

// GetPage returns a page by ID, or domain.ErrNotFound.
func GetPage(ctx context.Context, tx Tx, id ulid.ID) (domain.Page, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Page{}, err
	}
	row, err := storefrontdb.New(tx).GetPage(ctx, storefrontdb.GetPageParams{StoreID: store, ID: id})
	if err != nil {
		return domain.Page{}, notFound(err, "get page")
	}
	return toPage(row)
}

// ListPages returns a kind's pages by slug, without sections or body.
func ListPages(ctx context.Context, tx Tx, kind domain.PageKind) ([]domain.Page, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := storefrontdb.New(tx).ListPageSummaries(ctx, storefrontdb.ListPageSummariesParams{StoreID: store, Kind: string(kind)})
	if err != nil {
		return nil, fmt.Errorf("storefront store: list pages: %w", err)
	}
	out := make([]domain.Page, len(rows))
	for i, r := range rows {
		out[i] = domain.Page{
			ID: r.ID, Kind: domain.PageKind(r.Kind), Slug: r.Slug, Title: r.Title, Status: domain.PageStatus(r.Status),
			Version: int(r.Version), Sections: []domain.Section{}, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		}
	}
	return out, nil
}

// CountPages counts a kind's pages.
func CountPages(ctx context.Context, tx Tx, kind domain.PageKind) (int, error) {
	store, err := current(ctx)
	if err != nil {
		return 0, err
	}
	n, err := storefrontdb.New(tx).CountPages(ctx, storefrontdb.CountPagesParams{StoreID: store, Kind: string(kind)})
	if err != nil {
		return 0, fmt.Errorf("storefront store: count pages: %w", err)
	}
	return int(n), nil
}

// InsertPage creates a page. A taken (kind, slug) is ErrSlugTaken.
func InsertPage(ctx context.Context, tx Tx, id ulid.ID, kind domain.PageKind, in domain.PageInput) (domain.Page, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Page{}, err
	}
	secs, err := json.Marshal(in.Sections)
	if err != nil {
		return domain.Page{}, fmt.Errorf("storefront store: encode sections: %w", err)
	}
	row, err := storefrontdb.New(tx).InsertPage(ctx, storefrontdb.InsertPageParams{
		ID: id, StoreID: store, Kind: string(kind), Slug: in.Slug, Title: in.Title, Description: in.Description,
		Sections: secs, Body: in.Body, Status: in.Status,
	})
	if postgres.IsUniqueViolation(err, "store_pages_store_kind_slug_key") {
		return domain.Page{}, ErrSlugTaken
	}
	if err != nil {
		return domain.Page{}, fmt.Errorf("storefront store: insert page: %w", err)
	}
	return toPage(row)
}

// UpdatePage replaces the version of page id the caller read. A missing
// page or a newer version is domain.ErrVersionConflict; a taken slug is
// ErrSlugTaken.
func UpdatePage(ctx context.Context, tx Tx, id ulid.ID, in domain.PageInput) (domain.Page, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Page{}, err
	}
	secs, err := json.Marshal(in.Sections)
	if err != nil {
		return domain.Page{}, fmt.Errorf("storefront store: encode sections: %w", err)
	}
	row, err := storefrontdb.New(tx).UpdatePage(ctx, storefrontdb.UpdatePageParams{
		StoreID: store, ID: id, Version: int32(in.Version), //nolint:gosec // a row's version, far below 2^31
		Slug: in.Slug, Title: in.Title, Description: in.Description, Sections: secs, Body: in.Body, Status: in.Status,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.Page{}, domain.ErrVersionConflict
	case postgres.IsUniqueViolation(err, "store_pages_store_kind_slug_key"):
		return domain.Page{}, ErrSlugTaken
	case err != nil:
		return domain.Page{}, fmt.Errorf("storefront store: update page: %w", err)
	}
	return toPage(row)
}

// DeletePage deletes a landing page and returns its slug, or
// domain.ErrNotFound.
func DeletePage(ctx context.Context, tx Tx, id ulid.ID) (string, error) {
	store, err := current(ctx)
	if err != nil {
		return "", err
	}
	slug, err := storefrontdb.New(tx).DeletePage(ctx, storefrontdb.DeletePageParams{StoreID: store, ID: id})
	if err != nil {
		return "", notFound(err, "delete page")
	}
	return slug, nil
}

func toPage(r storefrontdb.StorePage) (domain.Page, error) {
	var secs []domain.Section
	if err := json.Unmarshal(r.Sections, &secs); err != nil {
		return domain.Page{}, fmt.Errorf("storefront store: decode sections of %s: %w", r.ID, err)
	}
	if secs == nil {
		secs = []domain.Section{}
	}
	return domain.Page{
		ID: r.ID, Kind: domain.PageKind(r.Kind), Slug: r.Slug, Title: r.Title, Description: r.Description,
		Sections: secs, Body: r.Body, Status: domain.PageStatus(r.Status), Version: int(r.Version),
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}, nil
}

func notFound(err error, op string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return fmt.Errorf("storefront store: %s: %w", op, err)
}
