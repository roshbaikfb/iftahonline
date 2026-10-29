// Package store adapts the sqlc queries in marketingdb to domain types.
// Every function runs on a transaction opened by postgres.InTenantTx and
// takes the store ID from tenant.FromContext, never from its arguments
// (CLAUDE.md rule 5); RLS checks it a second time.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/store/marketingdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Tx is the tenant transaction the functions run on.
type Tx = marketingdb.DBTX

// ErrStale means the row changed since the version the caller read.
var ErrStale = errors.New("marketing store: stale version")

// Row is a pixel as stored: the domain view plus the row's ID and its
// sealed token, which only this module's service opens.
type Row struct {
	domain.Pixel
	ID    ulid.ID
	Token []byte
}

// Write is what Save stores.
type Write struct {
	Provider domain.Provider
	ID       ulid.ID // a new row's; ignored on update
	PixelID  string
	Label    string
	TestCode string
	Enabled  bool
	Token    []byte // sealed; nil for none
	Version  int    // read; 0 inserts
}

func current(ctx context.Context) (ulid.ID, error) {
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return ulid.ID{}, fmt.Errorf("marketing store: %w", err)
	}
	return id.ID, nil
}

func toRow(r marketingdb.StorePixel) Row {
	return Row{ID: r.ID, Token: r.ServerToken, Pixel: domain.Pixel{
		Provider: domain.Provider(r.Provider), PixelID: r.PixelID, Label: r.Label, TestCode: r.TestCode, Enabled: r.Enabled,
		HasServerToken: len(r.ServerToken) > 0, Version: int(r.Version), UpdatedAt: r.UpdatedAt,
	}}
}

// Pixels returns the store's pixels.
func Pixels(ctx context.Context, tx Tx) ([]Row, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := marketingdb.New(tx).ListPixels(ctx, store)
	if err != nil {
		return nil, fmt.Errorf("marketing store: pixels: %w", err)
	}
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, toRow(r))
	}
	return out, nil
}

// Pixel returns one provider's pixel.
func Pixel(ctx context.Context, tx Tx, p domain.Provider) (Row, error) {
	store, err := current(ctx)
	if err != nil {
		return Row{}, err
	}
	r, err := marketingdb.New(tx).GetPixel(ctx, marketingdb.GetPixelParams{StoreID: store, Provider: string(p)})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Row{}, domain.ErrNotFound
	case err != nil:
		return Row{}, fmt.Errorf("marketing store: pixel: %w", err)
	}
	return toRow(r), nil
}

// Save inserts a pixel (Version 0) or updates it over the version read;
// ErrStale if another save came first.
func Save(ctx context.Context, tx Tx, w Write) (Row, error) {
	store, err := current(ctx)
	if err != nil {
		return Row{}, err
	}
	q := marketingdb.New(tx)
	var r marketingdb.StorePixel
	if w.Version == 0 {
		r, err = q.InsertPixel(ctx, marketingdb.InsertPixelParams{StoreID: store, Provider: string(w.Provider), ID: w.ID,
			PixelID: w.PixelID, Label: w.Label, TestCode: w.TestCode, Enabled: w.Enabled, ServerToken: w.Token})
	} else {
		r, err = q.UpdatePixel(ctx, marketingdb.UpdatePixelParams{StoreID: store, Provider: string(w.Provider),
			PixelID: w.PixelID, Label: w.Label, TestCode: w.TestCode, Enabled: w.Enabled, ServerToken: w.Token,
			Version: int32(w.Version)}) //nolint:gosec // a row's version, far below 2^31
	}
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Row{}, ErrStale
	case err != nil:
		return Row{}, fmt.Errorf("marketing store: save pixel: %w", err)
	}
	return toRow(r), nil
}

// DeletePixel removes a provider's pixel; domain.ErrNotFound if none.
func DeletePixel(ctx context.Context, tx Tx, p domain.Provider) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	n, err := marketingdb.New(tx).DeletePixel(ctx, marketingdb.DeletePixelParams{StoreID: store, Provider: string(p)})
	switch {
	case err != nil:
		return fmt.Errorf("marketing store: delete pixel: %w", err)
	case n == 0:
		return domain.ErrNotFound
	}
	return nil
}

// Settings returns the store's settings, or the defaults (version 0)
// before it saves any.
func Settings(ctx context.Context, tx Tx) (domain.Settings, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Settings{}, err
	}
	r, err := marketingdb.New(tx).GetSettings(ctx, store)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.DefaultSettings(), nil
	case err != nil:
		return domain.Settings{}, fmt.Errorf("marketing store: settings: %w", err)
	}
	return domain.Settings{Consent: domain.Consent(r.Consent), Version: int(r.Version)}, nil
}

// SaveSettings saves the settings over the version read (0: none yet);
// ErrStale if another save came first.
func SaveSettings(ctx context.Context, tx Tx, s domain.Settings) (domain.Settings, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Settings{}, err
	}
	q := marketingdb.New(tx)
	var r marketingdb.MarketingSetting
	if s.Version == 0 {
		r, err = q.InsertSettings(ctx, marketingdb.InsertSettingsParams{StoreID: store, Consent: string(s.Consent)})
	} else {
		r, err = q.UpdateSettings(ctx, marketingdb.UpdateSettingsParams{StoreID: store, Consent: string(s.Consent),
			Version: int32(s.Version)}) //nolint:gosec // a row's version
	}
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.Settings{}, ErrStale
	case err != nil:
		return domain.Settings{}, fmt.Errorf("marketing store: save settings: %w", err)
	}
	return domain.Settings{Consent: domain.Consent(r.Consent), Version: int(r.Version)}, nil
}
