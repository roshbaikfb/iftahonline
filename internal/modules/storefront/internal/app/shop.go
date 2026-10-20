package app

import (
	"context"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
)

// ShopContent is what every storefront page of a store needs, read in
// one transaction: the theme, the live logo and the published policies.
type ShopContent struct {
	Theme    domain.ThemeSettings
	Logo     *domain.Asset // nil without a logo
	Policies []domain.Page // published only, in display order, without bodies
}

// ShopContent reads the store's look for rendering.
func (s *Service) ShopContent(ctx context.Context) (ShopContent, error) {
	out := ShopContent{Theme: domain.DefaultTheme(), Policies: []domain.Page{}}
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		t, err := store.GetTheme(ctx, tx)
		switch {
		case err == nil:
			out.Theme = t.Settings
		case !errors.Is(err, domain.ErrNotFound):
			return err
		}
		if out.Logo, _, err = store.CurrentAssets(ctx, tx, domain.AssetLogo); err != nil {
			return err
		}
		pols, err := store.ListPages(ctx, tx, domain.KindPolicy)
		if err != nil {
			return err
		}
		for _, slug := range domain.PolicySlugs() {
			i := slices.IndexFunc(pols, func(p domain.Page) bool { return p.Slug == slug && p.Status == domain.StatusPublished })
			if i >= 0 {
				out.Policies = append(out.Policies, pols[i])
			}
		}
		return nil
	})
	return out, err
}

// PublishedLanding returns the published landing page at slug, or
// ErrPageNotFound (drafts are not shown to shoppers).
func (s *Service) PublishedLanding(ctx context.Context, slug string) (domain.Page, error) {
	return s.published(ctx, domain.KindLanding, slug)
}

// PublishedPolicy returns a published policy, or ErrPageNotFound.
func (s *Service) PublishedPolicy(ctx context.Context, policy string) (domain.Page, error) {
	if !slices.Contains(domain.PolicySlugs(), policy) {
		return domain.Page{}, ErrPageNotFound
	}
	return s.published(ctx, domain.KindPolicy, policy)
}

func (s *Service) published(ctx context.Context, kind domain.PageKind, slug string) (domain.Page, error) {
	var p domain.Page
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		p, txErr = store.GetPageByKey(ctx, tx, kind, slug)
		return txErr
	})
	if errors.Is(err, domain.ErrNotFound) || (err == nil && p.Status != domain.StatusPublished) {
		return domain.Page{}, ErrPageNotFound
	}
	return p, err
}
