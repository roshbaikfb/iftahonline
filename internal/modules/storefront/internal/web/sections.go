package web

import (
	"context"
	"fmt"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/sections"
	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// maxCategoryGrids bounds the catalog queries one page can cost: grids
// beyond it render empty.
const maxCategoryGrids = 6

// sectionView is a section with the data it shows.
type sectionView struct {
	id        string
	hero      *heroView
	grid      *gridView
	landing   *landingView
	reviews   *sections.Reviews
	faq       *sections.FAQ
	badges    *sections.TrustBadges
	countdown *countdownView
}

type heroView struct {
	cfg   *sections.Hero
	image *image
}

type gridView struct {
	title    string
	products []catalog.StorefrontProduct
	more     string // link to all of them, when the grid is a category's
}

type landingView struct {
	cfg     *sections.ProductLanding
	product catalog.StorefrontProduct
	areas   []geo.Area // the order form's governorates
}

type countdownView struct {
	cfg  *sections.Countdown
	left [4]int // days, hours, minutes, seconds at render time
	hide bool
}

// loadSections decodes a page's sections and fetches what they show in
// a bounded number of catalog calls: one for every product picked by
// ID, one for the newest products, one per category grid (at most
// maxCategoryGrids). Products that are gone or not active simply do not
// appear.
func (s *Shop) loadSections(ctx context.Context, p *pageCtx, secs []domain.Section, now time.Time) ([]sectionView, error) {
	decoded := make([]any, len(secs))
	var ids []ulid.ID
	newest := 0
	var cats []ulid.ID
	for i, sec := range secs {
		v, err := sections.Decode(sec.Type, sec.Settings)
		if err != nil {
			s.log.WarnContext(ctx, "storefront: skipping undecodable section", "section", sec.ID, "err", err)
			continue
		}
		decoded[i] = v
		switch c := v.(type) {
		case *sections.Hero:
			if c.ImageProductID != nil {
				ids = append(ids, *c.ImageProductID)
			}
		case *sections.ProductLanding:
			ids = append(ids, c.ProductID)
		case *sections.ProductGrid:
			switch c.Source {
			case "manual":
				ids = append(ids, c.ProductIDs...)
			case "category":
				if c.CategoryID != nil && len(cats) < maxCategoryGrids && !containsID(cats, *c.CategoryID) {
					cats = append(cats, *c.CategoryID)
				}
			default:
				newest = max(newest, c.Limit)
			}
		}
	}

	byID := map[ulid.ID]catalog.StorefrontProduct{}
	if len(ids) > 0 {
		ps, err := s.catalog.ProductsByIDs(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("web: products: %w", err)
		}
		for _, pr := range ps {
			byID[pr.ID] = pr
		}
	}
	var newestItems []catalog.StorefrontProduct
	if newest > 0 {
		page, err := s.catalog.ListProducts(ctx, catalog.StorefrontQuery{Limit: newest})
		if err != nil {
			return nil, fmt.Errorf("web: newest products: %w", err)
		}
		newestItems = page.Items
	}
	inCat := map[ulid.ID][]catalog.StorefrontProduct{}
	for _, id := range cats {
		page, err := s.catalog.ListProducts(ctx, catalog.StorefrontQuery{CategoryID: &id, Limit: sections.MaxGridLimit})
		if err != nil {
			return nil, fmt.Errorf("web: category products: %w", err)
		}
		inCat[id] = page.Items
	}

	out := make([]sectionView, 0, len(secs))
	for i, v := range decoded {
		sv := sectionView{id: secs[i].ID}
		switch c := v.(type) {
		case *sections.Hero:
			sv.hero = &heroView{cfg: c}
			if c.ImageProductID != nil {
				if pr, ok := byID[*c.ImageProductID]; ok {
					if img, ok := firstImage(pr); ok {
						sv.hero.image = &img
					}
					p.tag("p-" + pr.ID.String())
				}
			}
		case *sections.ProductGrid:
			sv.grid = s.grid(p, c, byID, newestItems, inCat)
		case *sections.ProductLanding:
			pr, ok := byID[c.ProductID]
			if !ok {
				continue // gone or not active: the section disappears
			}
			p.tag("p-" + pr.ID.String())
			sv.landing = &landingView{cfg: c, product: pr, areas: geo.Areas(p.v.country)}
		case *sections.Reviews:
			sv.reviews = c
		case *sections.FAQ:
			sv.faq = c
		case *sections.TrustBadges:
			sv.badges = c
		case *sections.Countdown:
			left := c.EndsAt.Sub(now)
			hide := c.HideWhenEnded == nil || *c.HideWhenEnded
			if left <= 0 && hide {
				continue
			}
			sv.countdown = &countdownView{cfg: c, left: clock(left), hide: hide}
		default:
			continue
		}
		out = append(out, sv)
	}
	return out, nil
}

func (s *Shop) grid(p *pageCtx, c *sections.ProductGrid, byID map[ulid.ID]catalog.StorefrontProduct,
	newest []catalog.StorefrontProduct, inCat map[ulid.ID][]catalog.StorefrontProduct) *gridView {
	g := &gridView{title: c.Title}
	switch c.Source {
	case "manual":
		for _, id := range c.ProductIDs {
			if pr, ok := byID[id]; ok {
				g.products = append(g.products, pr)
			}
		}
	case "category":
		if c.CategoryID != nil {
			g.products = inCat[*c.CategoryID]
			if cat, ok := p.v.categoryByID(*c.CategoryID); ok {
				g.more = categoryPath(cat.Slug)
				if g.title == "" {
					g.title = cat.Name
				}
			}
		}
	default:
		g.products = newest
		if g.title == "" {
			g.title = p.l.T(MsgNewArrivals)
		}
	}
	if len(g.products) > c.Limit {
		g.products = g.products[:c.Limit]
	}
	p.tagProducts(g.products)
	return g
}

func containsID(ids []ulid.ID, id ulid.ID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// clock splits a duration into days, hours, minutes and seconds.
func clock(d time.Duration) [4]int {
	if d < 0 {
		d = 0
	}
	s := int(d / time.Second)
	return [4]int{s / 86400, s / 3600 % 24, s / 60 % 60, s % 60}
}
