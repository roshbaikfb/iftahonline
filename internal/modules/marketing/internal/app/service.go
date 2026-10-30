// Package app holds the marketing use cases (ADR-0043): the merchant's
// pixels and consent mode, and what the storefront renders from them.
package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/capi"
	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/marketing/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/envelope"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// module names the sealed tokens' associated data.
const module = "marketing"

// errNoSealer is a role without the platform's keys asked to seal or
// open a token.
var errNoSealer = errors.New("marketing: no sealer on this role")

// Errors the HTTP layer renders.
var (
	ErrUnknownProvider = errs.NotFound("provider_not_found", "no such provider")
	ErrPixelNotFound   = errs.NotFound("pixel_not_found", "the store has no pixel for this provider")
	errStale           = errs.Conflict("stale_version", "changed since you read it: reload and try again")
)

// Sealer seals and opens tokens (platform/envelope).
type Sealer interface {
	Seal(ctx context.Context, plaintext, aad []byte) ([]byte, error)
	Open(ctx context.Context, blob, aad []byte) ([]byte, error)
}

var _ Sealer = (*envelope.Sealer)(nil)

// Service implements the use cases.
type Service struct {
	db      postgres.Beginner
	ids     *ulid.Generator
	sealer  Sealer
	events  eventbus.Publisher
	audit   audit.Recorder
	jobs    jobs.Inserter
	orders  Orders
	senders map[domain.Provider]capi.Sender
	plan    entitlements.Entitlements
}

// Deps are the service's dependencies.
type Deps struct {
	DB     postgres.Beginner
	IDs    *ulid.Generator
	Sealer Sealer
	// Events carries PixelsChanged (the storefront's caches follow).
	Events eventbus.Publisher
	// Audit records merchant actions; nil discards.
	Audit audit.Recorder
	// Jobs enqueues the server-side conversions; Orders reads an order's
	// Purchase; Senders send to each provider (nil: none are sent).
	Jobs    jobs.Inserter
	Orders  Orders
	Senders map[domain.Provider]capi.Sender
	// Plan says whether the store's plan includes pixels and feeds
	// (ADR-0046); nil includes them (tests).
	Plan entitlements.Entitlements
}

// NewService builds the service.
func NewService(d Deps) *Service {
	if d.Audit == nil {
		d.Audit = audit.Nop{}
	}
	if d.Events == nil {
		d.Events = eventbus.Nop{}
	}
	if d.Plan == nil {
		d.Plan = entitlements.Unrestricted()
	}
	return &Service{db: d.DB, ids: d.IDs, sealer: d.Sealer, events: d.Events, audit: d.Audit, jobs: d.Jobs, orders: d.Orders, senders: d.Senders,
		plan: d.Plan}
}

func spec(p domain.Provider) (domain.Spec, error) {
	s, ok := domain.SpecOf(p)
	if !ok {
		return domain.Spec{}, ErrUnknownProvider
	}
	return s, nil
}

func mapErr(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return ErrPixelNotFound
	case errors.Is(err, store.ErrStale):
		return errStale
	}
	return err
}

// Pixels lists the store's saved pixels.
func (s *Service) Pixels(ctx context.Context) ([]domain.Pixel, error) {
	var out []domain.Pixel
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		rows, err := store.Pixels(ctx, tx)
		for _, r := range rows {
			out = append(out, r.Pixel)
		}
		return err
	})
	return out, err
}

// SavePixel connects a provider or changes its pixel, over the version
// read.
func (s *Service) SavePixel(ctx context.Context, p domain.Provider, in domain.PixelInput) (domain.Pixel, error) {
	if err := s.plan.Require(ctx, entitlements.Pixels); err != nil {
		return domain.Pixel{}, err
	}
	sp, err := spec(p)
	if err != nil {
		return domain.Pixel{}, err
	}
	in = in.Normalize()
	if err = in.Validate(sp); err != nil {
		return domain.Pixel{}, err
	}
	storeID, err := tenant.FromContext(ctx)
	if err != nil {
		return domain.Pixel{}, fmt.Errorf("marketing: %w", err)
	}
	if s.sealer == nil {
		return domain.Pixel{}, errNoSealer
	}
	var out domain.Pixel
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		w := store.Write{Provider: p, PixelID: in.PixelID, Label: in.Label, TestCode: in.TestCode, Enabled: in.Enabled, Version: in.Version}
		if in.Version == 0 {
			w.ID = s.ids.New()
		} else {
			old, getErr := store.Pixel(ctx, tx, p)
			if errors.Is(getErr, domain.ErrNotFound) {
				return store.ErrStale // it was removed since it was read
			}
			if getErr != nil {
				return getErr
			}
			w.ID, w.Token = old.ID, old.Token
		}
		switch {
		case !sp.Server || in.ServerToken != nil && *in.ServerToken == "":
			w.Token = nil
		case in.ServerToken != nil:
			sealed, sealErr := s.sealer.Seal(ctx, []byte(*in.ServerToken), envelope.ConnectionAAD(module, string(p), storeID, w.ID))
			if sealErr != nil {
				return fmt.Errorf("marketing: seal token: %w", sealErr)
			}
			w.Token = sealed
		}
		row, saveErr := store.Save(ctx, tx, w)
		if saveErr != nil {
			return saveErr
		}
		out = row.Pixel
		if saveErr = s.changed(ctx, tx, p); saveErr != nil {
			return saveErr
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "pixel.saved", TargetType: "pixel", TargetID: string(p),
			Metadata: map[string]any{"pixel_id": in.PixelID, "enabled": in.Enabled, "server_token": out.HasServerToken}})
	})
	return out, mapErr(err)
}

// DeletePixel disconnects a provider, with its token.
func (s *Service) DeletePixel(ctx context.Context, p domain.Provider) error {
	if _, err := spec(p); err != nil {
		return err
	}
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if err := store.DeletePixel(ctx, tx, p); err != nil {
			return err
		}
		if err := s.changed(ctx, tx, p); err != nil {
			return err
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "pixel.deleted", TargetType: "pixel", TargetID: string(p)})
	})
	return mapErr(err)
}

// Settings returns the store's marketing settings.
func (s *Service) Settings(ctx context.Context) (domain.Settings, error) {
	var out domain.Settings
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (err error) {
		out, err = store.Settings(ctx, tx)
		return err
	})
	return out, err
}

// SaveSettings changes the consent mode, over the version read.
func (s *Service) SaveSettings(ctx context.Context, in domain.Settings) (domain.Settings, error) {
	if !in.Consent.Valid() {
		return domain.Settings{}, errs.Validation(errs.FieldError{Field: "consent", Code: "invalid", Message: "auto, required or off"})
	}
	if in.Version < 0 {
		return domain.Settings{}, errs.Validation(errs.FieldError{Field: "version", Code: "range", Message: "0 or more"})
	}
	var out domain.Settings
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (err error) {
		if out, err = store.SaveSettings(ctx, tx, in); err != nil {
			return err
		}
		if err = s.changed(ctx, tx, ""); err != nil {
			return err
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "marketing_settings.updated", TargetType: "marketing_settings",
			Metadata: map[string]any{"consent": string(in.Consent)}})
	})
	return out, mapErr(err)
}

// Browser is what the store's pages render: its enabled pixels' public
// IDs, its consent mode and the origins they need. No token is read.
// Without pixels in the store's plan, its pages load none.
func (s *Service) Browser(ctx context.Context) (domain.Browser, error) {
	var out domain.Browser
	allowed, pErr := s.plan.Allowed(ctx, entitlements.Pixels)
	if pErr != nil {
		return out, pErr
	}
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		rows, err := store.Pixels(ctx, tx)
		if err != nil {
			return err
		}
		set, err := store.Settings(ctx, tx)
		if err != nil {
			return err
		}
		if !allowed {
			rows = nil
		}
		pixels := make([]domain.Pixel, 0, len(rows))
		for _, r := range rows {
			pixels = append(pixels, r.Pixel)
		}
		out = domain.BrowserOf(pixels, set)
		return nil
	})
	return out, err
}

func (s *Service) changed(ctx context.Context, tx pgx.Tx, p domain.Provider) error {
	meta, err := event.MetaFor(ctx, s.ids, domain.EventPixelsChanged, 1)
	if err != nil {
		return err
	}
	if err = s.events.PublishTx(ctx, tx, domain.PixelsChanged{Meta: meta, Provider: p}); err != nil {
		return fmt.Errorf("marketing: publish: %w", err)
	}
	return nil
}
