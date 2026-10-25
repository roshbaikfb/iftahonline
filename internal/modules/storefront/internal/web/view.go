// Package web renders the storefront for shoppers: server-side HTML with
// templ, Arabic and English, from the store's theme and pages and the
// catalog's read side (ADR-0018). Pages carry no per-shopper state, so
// whole responses are cached in memory and at the edge.
package web

import (
	"context"
	"fmt"
	"strings"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy"
	"github.com/roshbaikfb/iftahonline/internal/platform/geo"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Catalog is the catalog's read side (catalog.Storefront).
type Catalog interface {
	ProductBySlug(ctx context.Context, slug string) (catalog.StorefrontProduct, error)
	ProductsByIDs(ctx context.Context, ids []ulid.ID) ([]catalog.StorefrontProduct, error)
	ListProducts(ctx context.Context, q catalog.StorefrontQuery) (catalog.StorefrontPage, error)
	Categories(ctx context.Context) ([]catalog.Category, error)
	SellableVariants(ctx context.Context, ids []ulid.ID) ([]catalog.SellableVariant, error)
	Search(ctx context.Context, q catalog.StorefrontSearch) (catalog.StorefrontSearchResult, error)
}

// Stores reads the store in ctx (tenancy.Service).
type Stores interface {
	GetStore(ctx context.Context) (tenancy.Store, error)
}

// Content is the storefront's own read side (app.Service).
type Content interface {
	ShopContent(ctx context.Context) (app.ShopContent, error)
	GetHome(ctx context.Context) (domain.Page, error)
	PublishedLanding(ctx context.Context, slug string) (domain.Page, error)
	PublishedPolicy(ctx context.Context, policy string) (domain.Page, error)
}

// Signer builds signed image URLs (media.Signer).
type Signer interface {
	URL(objectKey string, size int, format string) string
}

var _ Content = (*app.Service)(nil)

// storeView is what every page of a store shares, cached per store.
type storeView struct {
	name       string
	country    geo.Country // from the store's currency: the order form's governorates
	locale     Locale      // the store's default language, served without a prefix
	theme      domain.ThemeSettings
	css        string
	styleHash  string
	logo       *logoView
	categories []catalog.Category
	policies   []domain.Page
	cards      bool // checkout offers card payment: the footer says so
}

type logoView struct {
	avif, webp    string
	width, height int
}

// category finds a category by slug.
func (v *storeView) category(slug string) (catalog.Category, bool) {
	for _, c := range v.categories {
		if c.Slug == slug {
			return c, true
		}
	}
	return catalog.Category{}, false
}

// categoryByID finds a category by ID.
func (v *storeView) categoryByID(id ulid.ID) (catalog.Category, bool) {
	for _, c := range v.categories {
		if c.ID == id {
			return c, true
		}
	}
	return catalog.Category{}, false
}

// topCategories are the root categories, for the header.
func (v *storeView) topCategories() []catalog.Category {
	var out []catalog.Category
	for _, c := range v.categories {
		if c.ParentID == nil {
			out = append(out, c)
		}
	}
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

const logoBox = 400 // px: fits a 2.5rem-high header logo on 3x screens

func (s *Shop) buildView(ctx context.Context) (*storeView, error) {
	st, err := s.stores.GetStore(ctx)
	if err != nil {
		return nil, fmt.Errorf("web: store: %w", err)
	}
	content, err := s.content.ShopContent(ctx)
	if err != nil {
		return nil, fmt.Errorf("web: content: %w", err)
	}
	cats, err := s.catalog.Categories(ctx)
	if err != nil {
		return nil, fmt.Errorf("web: categories: %w", err)
	}
	country, _ := geo.CountryOf(st.Currency) // a store has one of the supported currencies
	v := &storeView{
		name: st.Name, country: country, locale: ParseLocale(string(st.Locale)), theme: content.Theme,
		categories: cats, policies: content.Policies,
	}
	v.cards = s.cardOffered(ctx, false)
	v.css = s.assets.themeCSS(content.Theme)
	v.styleHash = styleHash(v.css)
	if lg := content.Logo; lg != nil {
		w, h := lg.Width, lg.Height
		if w > logoBox || h > logoBox { // fitted like imgproxy's rs:fit
			if w >= h {
				w, h = logoBox, max(h*logoBox/lg.Width, 1)
			} else {
				w, h = max(w*logoBox/lg.Height, 1), logoBox
			}
		}
		v.logo = &logoView{avif: s.signer.URL(lg.ObjectKey, logoBox, "avif"), webp: s.signer.URL(lg.ObjectKey, logoBox, "webp"), width: w, height: h}
	}
	return v, nil
}

// cardBrands names the cards a store in country takes through its
// gateway: Visa and Mastercard everywhere, and the local scheme where
// there is one (Meeza in Egypt, mada in Saudi Arabia).
func cardBrands(l Locale, country geo.Country) string {
	local := map[geo.Country][2]string{geo.EG: {"ميزة", "Meeza"}, geo.SA: {"مدى", "mada"}}[country]
	brands := "Visa · Mastercard"
	switch {
	case local[0] == "":
	case l == English:
		brands += " · " + local[1]
	default:
		brands += " · " + local[0]
	}
	return brands
}

// paragraphs splits plain text into paragraphs at blank lines.
func paragraphs(s string) []string {
	var out []string
	for _, p := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n\n") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
