package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
)

var (
	errNoDraft         = errs.Conflict("no_draft", "there is nothing to publish")
	errTooManyPreviews = errs.Conflict("preview_limit", "too many open preview links; wait a few minutes")
)

// Preview is a short-lived link to the shop with the drafts: the shop
// address plus ?preview=Token.
type Preview struct {
	Token     string
	ExpiresAt time.Time
}

// GetDraft returns the store's unpublished edits (ADR-0036).
func (s *Service) GetDraft(ctx context.Context) (domain.Draft, error) {
	var d domain.Draft
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		d, txErr = store.GetDraft(ctx, tx)
		return txErr
	})
	return d, err
}

// SaveThemeDraft keeps the theme being edited; shoppers see nothing of
// it until PublishTheme.
func (s *Service) SaveThemeDraft(ctx context.Context, in domain.ThemeSettings) (domain.Draft, error) {
	in, err := domain.ValidateTheme(in)
	if err != nil {
		return domain.Draft{}, err
	}
	var d domain.Draft
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		d, txErr = store.SaveThemeDraft(ctx, tx, in)
		return txErr
	})
	return d, err
}

// SaveHomeDraft keeps the home page being edited, validated as the home
// page will be when published.
func (s *Service) SaveHomeDraft(ctx context.Context, in domain.HomeDraft) (domain.Draft, error) {
	v, err := s.validate(domain.KindHome, "", homeInput(in, 0))
	if err != nil {
		return domain.Draft{}, err
	}
	var d domain.Draft
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		d, txErr = store.SaveHomeDraft(ctx, tx, domain.HomeDraft{Title: v.Title, Description: v.Description, Sections: v.Sections})
		return txErr
	})
	return d, err
}

// DiscardThemeDraft drops the theme draft; the published theme stays.
func (s *Service) DiscardThemeDraft(ctx context.Context) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error { return store.ClearThemeDraft(ctx, tx) })
}

// DiscardHomeDraft drops the home page draft.
func (s *Service) DiscardHomeDraft(ctx context.Context) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error { return store.ClearHomeDraft(ctx, tx) })
}

// PublishTheme makes the theme draft the live theme, over the published
// version the caller read, and drops the draft, in one transaction.
func (s *Service) PublishTheme(ctx context.Context, version int) (domain.Theme, error) {
	var t domain.Theme
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		d, err := store.GetDraft(ctx, tx)
		if err != nil {
			return err
		}
		if d.Theme == nil {
			return errNoDraft
		}
		// Rules may have tightened since the draft was saved.
		in, err := domain.ValidateTheme(*d.Theme)
		if err != nil {
			return err
		}
		if t, err = s.saveThemeTx(ctx, tx, in, version, "theme.published"); err != nil {
			return err
		}
		return store.ClearThemeDraft(ctx, tx)
	})
	return t, err
}

// PublishHome makes the home page draft live over the version the caller
// read, and drops the draft.
func (s *Service) PublishHome(ctx context.Context, version int) (domain.Page, error) {
	var p domain.Page
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		d, err := store.GetDraft(ctx, tx)
		if err != nil {
			return err
		}
		if d.Home == nil {
			return errNoDraft
		}
		in, err := s.validate(domain.KindHome, "", homeInput(*d.Home, version))
		if err != nil {
			return err
		}
		if p, err = s.saveByKeyTx(ctx, tx, domain.KindHome, in); err != nil {
			return err
		}
		if err = store.ClearHomeDraft(ctx, tx); err != nil {
			return err
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "home.published", TargetType: "page", TargetID: p.ID.String()})
	})
	return p, err
}

func homeInput(h domain.HomeDraft, version int) domain.PageInput {
	return domain.PageInput{Slug: domain.HomeSlug, Title: h.Title, Description: h.Description, Sections: h.Sections, Version: version}
}

// CreatePreview makes a link that opens the shop with the drafts for
// domain.PreviewTTL. Only its hash is stored.
func (s *Service) CreatePreview(ctx context.Context) (Preview, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Preview{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	p := Preview{Token: token, ExpiresAt: time.Now().Add(domain.PreviewTTL).UTC().Truncate(time.Second)}
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		return store.InsertPreview(ctx, tx, sum[:], p.ExpiresAt)
	})
	if errors.Is(err, store.ErrTooManyPreviews) {
		return Preview{}, errTooManyPreviews
	}
	return p, err
}

// PreviewDraft returns the drafts a live preview token opens; false when
// the token is unknown, expired or another store's.
func (s *Service) PreviewDraft(ctx context.Context, token string) (domain.Draft, bool, error) {
	if token == "" || len(token) > 64 {
		return domain.Draft{}, false, nil
	}
	sum := sha256.Sum256([]byte(token))
	var d domain.Draft
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		d, txErr = store.PreviewDraft(ctx, tx, sum[:])
		return txErr
	})
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Draft{}, false, nil
	}
	return d, err == nil, err
}
