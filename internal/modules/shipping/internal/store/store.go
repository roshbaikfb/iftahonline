// Package store adapts the sqlc queries in shippingdb to domain types.
// Every function runs on a transaction opened by postgres.InTenantTx and
// takes the store ID from tenant.FromContext, never from its arguments
// (CLAUDE.md rule 5); RLS checks it a second time.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/store/shippingdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Tx is the tenant transaction the functions run on.
type Tx = shippingdb.DBTX

// Errors.
var (
	// ErrVersionConflict: the version read is stale.
	ErrVersionConflict = errors.New("shipping store: version conflict")
	// ErrAreaTaken: another zone already holds a governorate.
	ErrAreaTaken = errors.New("shipping store: area in another zone")
)

func current(ctx context.Context) (ulid.ID, error) {
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return ulid.ID{}, fmt.Errorf("shipping store: %w", err)
	}
	return id.ID, nil
}

// Load reads the store's settings and zones with their areas and cities
// in four queries, however many zones there are.
func Load(ctx context.Context, tx Tx) (domain.Settings, []domain.Zone, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Settings{}, nil, err
	}
	q := shippingdb.New(tx)
	var s domain.Settings
	row, err := q.GetSettings(ctx, store)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return domain.Settings{}, nil, fmt.Errorf("shipping store: settings: %w", err)
	default:
		s = toSettings(row)
	}
	rows, err := q.ListZones(ctx, store)
	if err != nil {
		return domain.Settings{}, nil, fmt.Errorf("shipping store: zones: %w", err)
	}
	zones := make([]domain.Zone, len(rows))
	index := make(map[ulid.ID]int, len(rows))
	for i, r := range rows {
		zones[i] = toZone(r)
		index[r.ID] = i
	}
	areas, err := q.ListZoneAreas(ctx, store)
	if err != nil {
		return domain.Settings{}, nil, fmt.Errorf("shipping store: areas: %w", err)
	}
	for _, a := range areas {
		if i, ok := index[a.ZoneID]; ok {
			zones[i].Areas = append(zones[i].Areas, a.Area) // sorted by the query
		}
	}
	cities, err := q.ListCityRates(ctx, store)
	if err != nil {
		return domain.Settings{}, nil, fmt.Errorf("shipping store: cities: %w", err)
	}
	for _, c := range cities {
		if i, ok := index[c.ZoneID]; ok {
			zones[i].Cities = append(zones[i].Cities, domain.CityRate{Name: c.CityName, Key: c.CityKey, RateMinor: c.RateMinor})
		}
	}
	return s, zones, nil
}

// SaveSettings writes the settings if version is the one read (0: never
// saved), returning them with their new version.
func SaveSettings(ctx context.Context, tx Tx, s domain.Settings, version int) (domain.Settings, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Settings{}, err
	}
	q := shippingdb.New(tx)
	var row shippingdb.ShippingSetting
	if version == 0 {
		row, err = q.InsertSettings(ctx, shippingdb.InsertSettingsParams{StoreID: store, CodFixedMinor: s.CODFee.FixedMinor,
			CodBp: int32(s.CODFee.BP), CodMinMinor: s.CODFee.MinMinor, CodMaxMinor: s.CODFee.MaxMinor, FreeOverMinor: s.FreeOverMinor}) //nolint:gosec // bp ≤ MaxBP
	} else {
		row, err = q.UpdateSettings(ctx, shippingdb.UpdateSettingsParams{StoreID: store, Version: int32(version), //nolint:gosec // a version read from the database
			CodFixedMinor: s.CODFee.FixedMinor, CodBp: int32(s.CODFee.BP), CodMinMinor: s.CODFee.MinMinor, //nolint:gosec // bp ≤ MaxBP
			CodMaxMinor: s.CODFee.MaxMinor, FreeOverMinor: s.FreeOverMinor})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Settings{}, ErrVersionConflict
	}
	if err != nil {
		return domain.Settings{}, fmt.Errorf("shipping store: save settings: %w", err)
	}
	return toSettings(row), nil
}

// Lock serializes the store's zone writes until the transaction ends.
func Lock(ctx context.Context, tx Tx) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if err = shippingdb.New(tx).LockZones(ctx, store); err != nil {
		return fmt.Errorf("shipping store: lock: %w", err)
	}
	return nil
}

// NextPosition returns the position after the store's last zone.
func NextPosition(ctx context.Context, tx Tx) (int, error) {
	store, err := current(ctx)
	if err != nil {
		return 0, err
	}
	n, err := shippingdb.New(tx).NextZonePosition(ctx, store)
	if err != nil {
		return 0, fmt.Errorf("shipping store: next position: %w", err)
	}
	return int(n), nil
}

// InsertZone creates z at position with its areas and cities.
func InsertZone(ctx context.Context, tx Tx, z domain.Zone) (domain.Zone, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Zone{}, err
	}
	fee := codColumns(z.CODFee)
	row, err := shippingdb.New(tx).InsertZone(ctx, shippingdb.InsertZoneParams{ID: z.ID, StoreID: store, Name: z.Name,
		RateMinor: z.RateMinor, FreeOverMinor: z.FreeOverMinor, CodFixedMinor: fee.fixed, CodBp: fee.bp, CodMinMinor: fee.min,
		CodMaxMinor: fee.max, Position: int32(z.Position)}) //nolint:gosec // < MaxZones
	if err != nil {
		return domain.Zone{}, fmt.Errorf("shipping store: insert zone: %w", err)
	}
	return withChildren(ctx, tx, store, row, z)
}

// UpdateZone replaces z (fields, areas, cities) if version is the one
// read; domain.ErrNotFound when it is gone.
func UpdateZone(ctx context.Context, tx Tx, z domain.Zone, version int) (domain.Zone, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Zone{}, err
	}
	q := shippingdb.New(tx)
	fee := codColumns(z.CODFee)
	row, err := q.UpdateZone(ctx, shippingdb.UpdateZoneParams{StoreID: store, ID: z.ID, Version: int32(version), Name: z.Name, //nolint:gosec // a version read from the database
		RateMinor: z.RateMinor, FreeOverMinor: z.FreeOverMinor, CodFixedMinor: fee.fixed, CodBp: fee.bp, CodMinMinor: fee.min, CodMaxMinor: fee.max})
	if errors.Is(err, pgx.ErrNoRows) {
		found, existsErr := q.ZoneExists(ctx, shippingdb.ZoneExistsParams{StoreID: store, ID: z.ID})
		switch {
		case existsErr != nil:
			return domain.Zone{}, fmt.Errorf("shipping store: zone exists: %w", existsErr)
		case found:
			return domain.Zone{}, ErrVersionConflict
		}
		return domain.Zone{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Zone{}, fmt.Errorf("shipping store: update zone: %w", err)
	}
	if err = q.DeleteZoneAreas(ctx, shippingdb.DeleteZoneAreasParams{StoreID: store, ZoneID: z.ID}); err != nil {
		return domain.Zone{}, fmt.Errorf("shipping store: clear areas: %w", err)
	}
	if err = q.DeleteCityRates(ctx, shippingdb.DeleteCityRatesParams{StoreID: store, ZoneID: z.ID}); err != nil {
		return domain.Zone{}, fmt.Errorf("shipping store: clear cities: %w", err)
	}
	return withChildren(ctx, tx, store, row, z)
}

func withChildren(ctx context.Context, tx Tx, store ulid.ID, row shippingdb.ShippingZone, z domain.Zone) (domain.Zone, error) {
	q := shippingdb.New(tx)
	if err := q.InsertZoneAreas(ctx, shippingdb.InsertZoneAreasParams{StoreID: store, ZoneID: z.ID, Areas: z.Areas}); err != nil {
		if postgres.IsUniqueViolation(err, "shipping_zone_areas_pkey") {
			return domain.Zone{}, ErrAreaTaken
		}
		return domain.Zone{}, fmt.Errorf("shipping store: insert areas: %w", err)
	}
	if len(z.Cities) > 0 {
		p := shippingdb.InsertCityRatesParams{StoreID: store, ZoneID: z.ID}
		for _, c := range z.Cities {
			p.Keys, p.Names, p.Rates = append(p.Keys, c.Key), append(p.Names, c.Name), append(p.Rates, c.RateMinor)
		}
		if err := q.InsertCityRates(ctx, p); err != nil {
			return domain.Zone{}, fmt.Errorf("shipping store: insert cities: %w", err)
		}
	}
	out := toZone(row)
	out.Areas, out.Cities = z.Areas, z.Cities
	return out, nil
}

// DeleteZone deletes a zone with its areas and cities; domain.ErrNotFound
// when it does not exist.
func DeleteZone(ctx context.Context, tx Tx, id ulid.ID) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	n, err := shippingdb.New(tx).DeleteZone(ctx, shippingdb.DeleteZoneParams{StoreID: store, ID: id})
	if err != nil {
		return fmt.Errorf("shipping store: delete zone: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func toSettings(r shippingdb.ShippingSetting) domain.Settings {
	return domain.Settings{CODFee: domain.CODFee{FixedMinor: r.CodFixedMinor, BP: int64(r.CodBp), MinMinor: r.CodMinMinor, MaxMinor: r.CodMaxMinor},
		FreeOverMinor: r.FreeOverMinor, Version: int(r.Version)}
}

func toZone(r shippingdb.ShippingZone) domain.Zone {
	z := domain.Zone{ID: r.ID, Name: r.Name, RateMinor: r.RateMinor, FreeOverMinor: r.FreeOverMinor,
		Position: int(r.Position), Version: int(r.Version)}
	if r.CodFixedMinor != nil && r.CodBp != nil && r.CodMinMinor != nil && r.CodMaxMinor != nil {
		z.CODFee = &domain.CODFee{FixedMinor: *r.CodFixedMinor, BP: int64(*r.CodBp), MinMinor: *r.CodMinMinor, MaxMinor: *r.CodMaxMinor}
	}
	return z
}

type codCols struct {
	fixed, min, max *int64
	bp              *int32
}

func codColumns(f *domain.CODFee) codCols {
	if f == nil {
		return codCols{}
	}
	bp := int32(f.BP) //nolint:gosec // ≤ MaxBP
	return codCols{fixed: &f.FixedMinor, bp: &bp, min: &f.MinMinor, max: &f.MaxMinor}
}
