// Package app holds the shipping use cases (ADR-0025): merchants read and
// change their settings and zones; orders and the storefront quote
// deliveries from a cached copy of the store's config. Every change
// writes an audit entry and publishes shipping.rates.changed in its
// transaction, which invalidates the caches of every instance.
package app

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/shipping/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/envelope"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Errors. Match with errors.Is.
var (
	ErrZoneNotFound    = errs.NotFound("zone_not_found", "shipping zone not found")
	ErrVersionConflict = errs.Conflict("version_conflict", "the shipping settings changed since they were read; reload and try again")
	errAreaTaken       = errs.Validation(errs.FieldError{Field: "areas", Code: "taken",
		Message: "a governorate can be in one zone only; remove it from its other zone first"})
)

// StoreInfo is what shipping needs about the store in ctx.
type StoreInfo struct {
	Currency money.Currency
	Country  geo.Country
}

// StoreSource returns the StoreInfo of the store in ctx; cmd/platform
// adapts the tenancy module to it.
type StoreSource func(ctx context.Context) (StoreInfo, error)

// Service implements the use cases.
type Service struct {
	db     postgres.Beginner
	ids    *ulid.Generator
	audit  audit.Recorder
	events eventbus.Publisher
	stores StoreSource
	cache  *configs

	carriers  Carriers
	sealer    *envelope.Sealer
	publicURL string
}

// Deps are the service's dependencies.
type Deps struct {
	DB     postgres.Beginner
	IDs    *ulid.Generator
	Audit  audit.Recorder
	Events eventbus.Publisher
	Stores StoreSource
	Cache  CacheConfig
	// Carriers are the adapters merchants may connect.
	Carriers Carriers
	// Sealer seals carrier credentials (platform/envelope).
	Sealer *envelope.Sealer
	// PublicURL is the platform's public origin, for webhook URLs.
	PublicURL string
}

// NewService builds the service. A nil Audit or Events discards.
func NewService(d Deps) *Service {
	if d.Audit == nil {
		d.Audit = audit.Nop{}
	}
	if d.Events == nil {
		d.Events = eventbus.Nop{}
	}
	return &Service{db: d.DB, ids: d.IDs, audit: d.Audit, events: d.Events, stores: d.Stores, cache: newConfigs(d.Cache),
		carriers: d.Carriers, sealer: d.Sealer, publicURL: d.PublicURL}
}

// Config returns the store's settings and zones as stored now (for the
// merchant, who must see their own writes).
func (s *Service) Config(ctx context.Context) (domain.Config, error) {
	info, err := s.stores(ctx)
	if err != nil {
		return domain.Config{}, err
	}
	cfg := domain.Config{Currency: info.Currency}
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		cfg.Settings, cfg.Zones, txErr = store.Load(ctx, tx)
		return txErr
	})
	return cfg, err
}

// Quote prices delivering an order of the store in ctx, from the cached
// config (at most the cache TTL old when an invalidation is lost).
func (s *Service) Quote(ctx context.Context, req domain.QuoteRequest) (domain.Quote, error) {
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return domain.Quote{}, err
	}
	cfg, key, ok := s.cache.get(id)
	if !ok {
		if cfg, err = s.Config(ctx); err != nil {
			return domain.Quote{}, err
		}
		s.cache.put(key, cfg)
	}
	return cfg.Quote(req)
}

// QuoteMinor is Quote for a subtotal in minor units of the store's
// currency (the merchant API's preview).
func (s *Service) QuoteMinor(ctx context.Context, governorate, city string, subtotalMinor int64) (domain.Quote, error) {
	info, err := s.stores(ctx)
	if err != nil {
		return domain.Quote{}, err
	}
	sub, err := money.New(subtotalMinor, info.Currency)
	if err != nil {
		return domain.Quote{}, errs.Validation(errs.FieldError{Field: "subtotal_minor", Code: "range", Message: "invalid amount"})
	}
	return s.Quote(ctx, domain.QuoteRequest{Governorate: governorate, City: city, Subtotal: sub})
}

// Invalidate drops this instance's cached config of store.
func (s *Service) Invalidate(store tenant.StoreID) { s.cache.invalidate(store) }

// SaveSettings replaces the store-wide settings. version is the one read
// (0 when never saved).
func (s *Service) SaveSettings(ctx context.Context, in domain.Settings, version int) (domain.Settings, error) {
	if err := domain.ValidateSettings(in); err != nil {
		return domain.Settings{}, err
	}
	var out domain.Settings
	err := s.change(ctx, "shipping_settings.updated", "shipping_settings", "", func(tx pgx.Tx) (txErr error) {
		out, txErr = store.SaveSettings(ctx, tx, in, version)
		return txErr
	})
	return out, err
}

// CreateZone adds a zone after the store's others. A governorate is in
// one zone only (the database's key on it); zones need a governorate
// each, so a store has at most as many zones as its country has
// governorates.
func (s *Service) CreateZone(ctx context.Context, in domain.ZoneInput) (domain.Zone, error) {
	z, err := s.validZone(ctx, in)
	if err != nil {
		return domain.Zone{}, err
	}
	z.ID = s.ids.New()
	var out domain.Zone
	err = s.change(ctx, "shipping_zone.created", "shipping_zone", z.ID.String(), func(tx pgx.Tx) error {
		if txErr := store.Lock(ctx, tx); txErr != nil {
			return txErr
		}
		next, txErr := store.NextPosition(ctx, tx)
		if txErr != nil {
			return txErr
		}
		z.Position = next
		out, txErr = store.InsertZone(ctx, tx, z)
		return txErr
	})
	return out, err
}

// UpdateZone replaces a zone's name, rates, governorates and cities.
// version is the one read.
func (s *Service) UpdateZone(ctx context.Context, id ulid.ID, in domain.ZoneInput, version int) (domain.Zone, error) {
	z, err := s.validZone(ctx, in)
	if err != nil {
		return domain.Zone{}, err
	}
	z.ID = id
	var out domain.Zone
	err = s.change(ctx, "shipping_zone.updated", "shipping_zone", id.String(), func(tx pgx.Tx) error {
		if txErr := store.Lock(ctx, tx); txErr != nil {
			return txErr
		}
		var txErr error
		out, txErr = store.UpdateZone(ctx, tx, z, version)
		return txErr
	})
	return out, err
}

// DeleteZone deletes a zone: its governorates are no longer delivered to
// (unless it was the last zone: then the store delivers everywhere).
func (s *Service) DeleteZone(ctx context.Context, id ulid.ID) error {
	return s.change(ctx, "shipping_zone.deleted", "shipping_zone", id.String(), func(tx pgx.Tx) error {
		if err := store.Lock(ctx, tx); err != nil {
			return err
		}
		return store.DeleteZone(ctx, tx, id)
	})
}

func (s *Service) validZone(ctx context.Context, in domain.ZoneInput) (domain.Zone, error) {
	info, err := s.stores(ctx)
	if err != nil {
		return domain.Zone{}, err
	}
	return domain.ValidateZone(in, info.Country)
}

// change runs fn with the audit entry and the rates-changed event in one
// transaction, then drops this instance's cached config.
func (s *Service) change(ctx context.Context, action, targetType, targetID string, fn func(pgx.Tx) error) error {
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return err
	}
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if txErr := fn(tx); txErr != nil {
			return txErr
		}
		if txErr := s.audit.RecordTx(ctx, tx, audit.Entry{Action: action, TargetType: targetType, TargetID: targetID}); txErr != nil {
			return txErr
		}
		m, txErr := event.MetaFor(ctx, s.ids, domain.EventRatesChanged, 1)
		if txErr != nil {
			return txErr
		}
		return s.events.PublishTx(ctx, tx, domain.RatesChanged{Meta: m})
	})
	switch {
	case errors.Is(err, store.ErrVersionConflict):
		return ErrVersionConflict
	case errors.Is(err, store.ErrAreaTaken):
		return errAreaTaken
	case errors.Is(err, domain.ErrNotFound):
		return ErrZoneNotFound
	case err != nil:
		return err
	}
	s.cache.invalidate(id)
	return nil
}
