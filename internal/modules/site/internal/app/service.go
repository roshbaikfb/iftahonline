// Package app holds the use cases of the platform site's content (S4a,
// ADR-0073): what the site reads, kept a minute in memory, and what the
// console edits.
package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// CacheTTL bounds how long a console change takes to reach the site's
// pages (approved: about a minute, then the edge's lifetime).
const CacheTTL = time.Minute

// loadTimeout bounds the shared read of the content.
const loadTimeout = 5 * time.Second

var (
	errNoConsole   = errs.Unavailable(0, errors.New("site: the console's pool is not wired"))
	errFAQNotFound = errs.NotFound("faq_not_found", "question not found")
	errTooMany     = errs.Conflict("too_many_faqs", "the FAQ holds at most 100 questions")
)

// Deps are the service's dependencies.
type Deps struct {
	DB    store.Tx // app_user: the site reads
	Admin store.Tx // app_admin: the console writes; nil outside the api role
	IDs   *ulid.Generator
	Log   *slog.Logger
	Now   func() time.Time
}

// Service implements the use cases.
type Service struct {
	d     Deps
	mu    sync.Mutex
	got   domain.Content
	until time.Time
	ok    bool // got was read once
	loads singleflight.Group
}

// NewService wires the service.
func NewService(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	return &Service{d: d}
}

// Content is the site's content, read at most once a minute and shared
// by every page. When the read fails, the last content read is kept
// (and logged); with none yet, the error is returned.
func (s *Service) Content(ctx context.Context) (domain.Content, error) {
	now := s.d.Now()
	s.mu.Lock()
	got, fresh, ok := s.got, now.Before(s.until), s.ok
	s.mu.Unlock()
	if fresh {
		return got, nil
	}
	v, err, _ := s.loads.Do("content", func() (any, error) {
		// Shared by every waiting page: one leaving does not cancel it.
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), loadTimeout)
		defer cancel()
		settings, err := store.Settings(lctx, s.d.DB)
		if err != nil {
			return nil, err
		}
		faqs, err := store.FAQs(lctx, s.d.DB)
		if err != nil {
			return nil, err
		}
		c := domain.Content{Settings: settings, FAQs: faqs}
		s.mu.Lock()
		s.got, s.until, s.ok = c, now.Add(CacheTTL), true
		s.mu.Unlock()
		return c, nil
	})
	if err != nil {
		if ok {
			s.d.Log.WarnContext(ctx, "site: content read failed, the last read kept", "err", err)
			return got, nil
		}
		return domain.Content{}, err
	}
	c, _ := v.(domain.Content) //nolint:errcheck // the loader returns only Content
	return c, nil
}

func (s *Service) admin() (store.Tx, error) {
	if s.d.Admin == nil {
		return nil, errNoConsole
	}
	return s.d.Admin, nil
}

// Settings are the site's contact details, as the console edits them.
func (s *Service) Settings(ctx context.Context) (domain.Settings, error) {
	db, err := s.admin()
	if err != nil {
		return domain.Settings{}, err
	}
	return store.Settings(ctx, db)
}

// SaveSettings checks and writes the contact details; it returns them as
// saved (trimmed).
func (s *Service) SaveSettings(ctx context.Context, set domain.Settings) (domain.Settings, error) {
	db, err := s.admin()
	if err != nil {
		return domain.Settings{}, err
	}
	set.Normalize()
	if err = set.Validate(); err != nil {
		return domain.Settings{}, err
	}
	return set, store.SaveSettings(ctx, db, set)
}

// FAQs are the questions in order, as the console edits them.
func (s *Service) FAQs(ctx context.Context) ([]domain.FAQ, error) {
	db, err := s.admin()
	if err != nil {
		return nil, err
	}
	return store.FAQs(ctx, db)
}

// CreateFAQ adds a question, with a new ID.
func (s *Service) CreateFAQ(ctx context.Context, f domain.FAQ) (domain.FAQ, error) {
	db, err := s.admin()
	if err != nil {
		return domain.FAQ{}, err
	}
	f.Normalize()
	if err = f.Validate(); err != nil {
		return domain.FAQ{}, err
	}
	all, err := store.FAQs(ctx, db)
	if err != nil {
		return domain.FAQ{}, err
	}
	if len(all) >= domain.MaxFAQs {
		return domain.FAQ{}, errTooMany
	}
	f.ID = s.d.IDs.New()
	return f, store.InsertFAQ(ctx, db, f)
}

// UpdateFAQ rewrites a question.
func (s *Service) UpdateFAQ(ctx context.Context, f domain.FAQ) (domain.FAQ, error) {
	db, err := s.admin()
	if err != nil {
		return domain.FAQ{}, err
	}
	f.Normalize()
	if err = f.Validate(); err != nil {
		return domain.FAQ{}, err
	}
	found, err := store.UpdateFAQ(ctx, db, f)
	if err != nil {
		return domain.FAQ{}, err
	}
	if !found {
		return domain.FAQ{}, errFAQNotFound
	}
	return f, nil
}

// DeleteFAQ removes a question.
func (s *Service) DeleteFAQ(ctx context.Context, id ulid.ID) error {
	db, err := s.admin()
	if err != nil {
		return err
	}
	found, err := store.DeleteFAQ(ctx, db, id)
	if err != nil {
		return err
	}
	if !found {
		return errFAQNotFound
	}
	return nil
}
