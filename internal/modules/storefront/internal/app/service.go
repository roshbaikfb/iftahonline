// Package app holds the storefront's merchant use cases: the theme, the
// home page, landing pages and policies. Every method acts on the store
// in ctx inside one tenant transaction, records the merchant's action in
// the audit log and publishes what changed in that same transaction, so
// caches of rendered pages learn of it (ADR-0015).
package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Errors.
var (
	ErrPageNotFound = errs.NotFound("page_not_found", "page not found")
	errPageLimit    = errs.Conflict("page_limit", fmt.Sprintf("a store can have at most %d landing pages", domain.MaxLandingPages))
)

// Service implements the use cases.
type Service struct {
	db       postgres.Beginner
	ids      *ulid.Generator
	audit    audit.Recorder
	events   eventbus.Publisher
	sections domain.SettingsValidator
	jobs     jobs.Inserter
	media    Media
	variants Variants
}

// Deps are the service's dependencies.
type Deps struct {
	DB       postgres.Beginner
	IDs      *ulid.Generator
	Audit    audit.Recorder
	Events   eventbus.Publisher
	Sections domain.SettingsValidator
	// Jobs and Media serve the logo (asset) use cases.
	Jobs  jobs.Inserter
	Media Media
	// Variants serves the cart (the catalog's sellable variants).
	Variants Variants
}

// NewService builds the service. A nil Audit or Events discards.
func NewService(d Deps) *Service {
	if d.Audit == nil {
		d.Audit = audit.Nop{}
	}
	if d.Events == nil {
		d.Events = eventbus.Nop{}
	}
	return &Service{db: d.DB, ids: d.IDs, audit: d.Audit, events: d.Events, sections: d.Sections, jobs: d.Jobs, media: d.Media, variants: d.Variants}
}

// GetTheme returns the saved theme, or the defaults at version 0.
func (s *Service) GetTheme(ctx context.Context) (domain.Theme, error) {
	var t domain.Theme
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		t, txErr = store.GetTheme(ctx, tx)
		return txErr
	})
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Theme{Settings: domain.DefaultTheme()}, nil
	}
	return t, err
}

// UpdateTheme saves the theme over the version the caller read (0 for
// the first save).
func (s *Service) UpdateTheme(ctx context.Context, in domain.ThemeSettings, version int) (domain.Theme, error) {
	in, err := domain.ValidateTheme(in)
	if err != nil {
		return domain.Theme{}, err
	}
	if version < 0 {
		return domain.Theme{}, errs.Validation(errs.FieldError{Field: "version", Code: "range", Message: "version must be 0 or more"})
	}
	var t domain.Theme
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		if t, txErr = store.SaveTheme(ctx, tx, in, version); txErr != nil {
			return txErr
		}
		if txErr = s.publish(ctx, tx, domain.EventThemeChanged, func(m event.Meta) event.Event { return domain.ThemeChanged{Meta: m} }); txErr != nil {
			return txErr
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "theme.updated", TargetType: "theme"})
	})
	return t, err
}

// GetHome returns the home page, or the default one at version 0.
func (s *Service) GetHome(ctx context.Context) (domain.Page, error) {
	return s.getByKey(ctx, domain.KindHome, domain.HomeSlug, domain.DefaultHome)
}

// SaveHome saves the home page over the version the caller read.
func (s *Service) SaveHome(ctx context.Context, in domain.PageInput) (domain.Page, error) {
	return s.saveByKey(ctx, domain.KindHome, "", in)
}

// ListPolicies returns every policy in display order, defaults included.
func (s *Service) ListPolicies(ctx context.Context) ([]domain.Page, error) {
	var saved []domain.Page
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		saved, txErr = store.ListPages(ctx, tx, domain.KindPolicy)
		return txErr
	})
	if err != nil {
		return nil, err
	}
	out := make([]domain.Page, 0, len(domain.PolicySlugs()))
	for _, slug := range domain.PolicySlugs() {
		i := slices.IndexFunc(saved, func(p domain.Page) bool { return p.Slug == slug })
		if i >= 0 {
			out = append(out, saved[i])
		} else {
			out = append(out, domain.DefaultPolicy(slug))
		}
	}
	return out, nil
}

// GetPolicy returns a policy, or its empty default at version 0.
func (s *Service) GetPolicy(ctx context.Context, policy string) (domain.Page, error) {
	if !slices.Contains(domain.PolicySlugs(), policy) {
		return domain.Page{}, ErrPageNotFound
	}
	return s.getByKey(ctx, domain.KindPolicy, policy, func() domain.Page { return domain.DefaultPolicy(policy) })
}

// SavePolicy saves a policy over the version the caller read.
func (s *Service) SavePolicy(ctx context.Context, policy string, in domain.PageInput) (domain.Page, error) {
	if !slices.Contains(domain.PolicySlugs(), policy) {
		return domain.Page{}, ErrPageNotFound
	}
	return s.saveByKey(ctx, domain.KindPolicy, policy, in)
}

// ListLandingPages returns the landing pages by slug, without sections.
func (s *Service) ListLandingPages(ctx context.Context) ([]domain.Page, error) {
	var out []domain.Page
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		out, txErr = store.ListPages(ctx, tx, domain.KindLanding)
		return txErr
	})
	return out, err
}

// CreateLandingPage creates a landing page (at most
// domain.MaxLandingPages per store).
func (s *Service) CreateLandingPage(ctx context.Context, in domain.PageInput) (domain.Page, error) {
	in, err := s.validate(domain.KindLanding, "", in)
	if err != nil {
		return domain.Page{}, err
	}
	var p domain.Page
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		n, txErr := store.CountPages(ctx, tx, domain.KindLanding)
		if txErr != nil {
			return txErr
		}
		if n >= domain.MaxLandingPages {
			return errPageLimit
		}
		if p, txErr = store.InsertPage(ctx, tx, s.ids.New(), domain.KindLanding, in); txErr != nil {
			return txErr
		}
		return s.pageChanged(ctx, tx, "page.created", p, "", false)
	})
	return p, err
}

// GetLandingPage returns a landing page.
func (s *Service) GetLandingPage(ctx context.Context, id ulid.ID) (domain.Page, error) {
	var p domain.Page
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		p, txErr = landing(ctx, tx, id)
		return txErr
	})
	return p, err
}

// UpdateLandingPage replaces a landing page over the version the caller
// read.
func (s *Service) UpdateLandingPage(ctx context.Context, id ulid.ID, in domain.PageInput) (domain.Page, error) {
	in, err := s.validate(domain.KindLanding, "", in)
	if err != nil {
		return domain.Page{}, err
	}
	var p domain.Page
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		old, txErr := landing(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		if p, txErr = store.UpdatePage(ctx, tx, id, in); txErr != nil {
			return txErr
		}
		previous := ""
		if old.Slug != p.Slug {
			previous = old.Slug
		}
		return s.pageChanged(ctx, tx, "page.updated", p, previous, false)
	})
	return p, err
}

// DeleteLandingPage deletes a landing page.
func (s *Service) DeleteLandingPage(ctx context.Context, id ulid.ID) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		slug, err := store.DeletePage(ctx, tx, id)
		if errors.Is(err, domain.ErrNotFound) {
			return ErrPageNotFound
		}
		if err != nil {
			return err
		}
		return s.pageChanged(ctx, tx, "page.deleted", domain.Page{ID: id, Kind: domain.KindLanding, Slug: slug}, "", true)
	})
}

// landing reads a page and hides any that is not a landing page.
func landing(ctx context.Context, tx pgx.Tx, id ulid.ID) (domain.Page, error) {
	p, err := store.GetPage(ctx, tx, id)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && p.Kind != domain.KindLanding) {
		return domain.Page{}, ErrPageNotFound
	}
	return p, err
}

func (s *Service) getByKey(ctx context.Context, kind domain.PageKind, slug string, def func() domain.Page) (domain.Page, error) {
	var p domain.Page
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		p, txErr = store.GetPageByKey(ctx, tx, kind, slug)
		return txErr
	})
	if errors.Is(err, domain.ErrNotFound) {
		return def(), nil
	}
	return p, err
}

// saveByKey saves the one page of kind at policy (or home): version 0
// creates it, and a page created meanwhile by someone else is a conflict.
func (s *Service) saveByKey(ctx context.Context, kind domain.PageKind, policy string, in domain.PageInput) (domain.Page, error) {
	in, err := s.validate(kind, policy, in)
	if err != nil {
		return domain.Page{}, err
	}
	var p domain.Page
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		action := "page.updated"
		if in.Version == 0 {
			action = "page.created"
			p, txErr = store.InsertPage(ctx, tx, s.ids.New(), kind, in)
			if errors.Is(txErr, store.ErrSlugTaken) {
				return domain.ErrVersionConflict
			}
		} else {
			var cur domain.Page
			if cur, txErr = store.GetPageByKey(ctx, tx, kind, in.Slug); errors.Is(txErr, domain.ErrNotFound) {
				return domain.ErrVersionConflict
			} else if txErr != nil {
				return txErr
			}
			p, txErr = store.UpdatePage(ctx, tx, cur.ID, in)
		}
		if txErr != nil {
			return txErr
		}
		return s.pageChanged(ctx, tx, action, p, "", false)
	})
	return p, err
}

func (s *Service) validate(kind domain.PageKind, policy string, in domain.PageInput) (domain.PageInput, error) {
	return domain.ValidatePage(kind, policy, in, s.sections, func() string { return strings.ToLower(s.ids.New().String()) })
}

func (s *Service) pageChanged(ctx context.Context, tx pgx.Tx, action string, p domain.Page, previousSlug string, deleted bool) error {
	err := s.publish(ctx, tx, domain.EventPageChanged, func(m event.Meta) event.Event {
		return domain.PageChanged{Meta: m, PageID: p.ID, Kind: p.Kind, Slug: p.Slug, PreviousSlug: previousSlug, Deleted: deleted}
	})
	if err != nil {
		return err
	}
	return s.audit.RecordTx(ctx, tx, audit.Entry{Action: action, TargetType: "page", TargetID: p.ID.String(),
		Metadata: map[string]any{"kind": string(p.Kind), "slug": p.Slug}})
}

func (s *Service) publish(ctx context.Context, tx pgx.Tx, typ string, mk func(event.Meta) event.Event) error {
	m, err := event.MetaFor(ctx, s.ids, typ, 1)
	if err != nil {
		return fmt.Errorf("storefront: publish %s: %w", typ, err)
	}
	return s.events.PublishTx(ctx, tx, mk(m))
}
