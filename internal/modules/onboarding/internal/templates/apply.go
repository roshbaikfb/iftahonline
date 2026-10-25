package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/onboarding/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Applier applies a starter template to the store in ctx. Every step is
// idempotent, so a job that runs twice leaves one copy of everything.
type Applier struct {
	Catalog    catalog.Starter
	Storefront storefront.Starter
	// Currency is the store's currency; prices are picked by it.
	Currency  func(ctx context.Context) (money.Currency, error)
	templates map[domain.Activity]Template
}

// NewApplier loads the templates; it fails if one is broken.
func NewApplier(c catalog.Starter, s storefront.Starter, currency func(ctx context.Context) (money.Currency, error)) (*Applier, error) {
	ts, err := Load()
	if err != nil {
		return nil, err
	}
	return &Applier{Catalog: c, Storefront: s, Currency: currency, templates: ts}, nil
}

// Apply fills the store from the activity's template and the merchant's
// look and colour: categories, sample products, the theme and home page,
// and draft policies. It adds no shipping zones (ADR-0034): the wizard's
// shipping step saves the merchant's own.
func (a *Applier) Apply(ctx context.Context, c domain.Choice) (int, error) {
	t, ok := a.templates[c.Activity]
	if !ok {
		return 0, fmt.Errorf("templates: no template for %q", c.Activity)
	}
	cur, err := a.Currency(ctx)
	if err != nil {
		return 0, err
	}
	catIDs := make(map[string]ulid.ID, len(t.Categories))
	pics := make(map[string]string, len(t.Categories))
	order := make([]ulid.ID, 0, len(t.Categories))
	for i, cat := range t.Categories {
		id, catErr := a.Catalog.EnsureCategory(ctx, catalog.CategoryInput{Slug: cat.Slug, Name: cat.Name, Position: i})
		if catErr != nil {
			return 0, fmt.Errorf("templates: category %s: %w", cat.Slug, catErr)
		}
		catIDs[cat.Slug], pics[cat.Slug] = id, cat.Image
		order = append(order, id)
	}
	var first ulid.ID
	for i, p := range t.Products {
		in, inErr := sample(p, cur, catIDs[p.Category], pics[p.Category])
		if inErr != nil {
			return 0, inErr
		}
		id, prodErr := a.Catalog.EnsureSampleProduct(ctx, in)
		if prodErr != nil {
			return 0, fmt.Errorf("templates: product %s: %w", p.Slug, prodErr)
		}
		if i == 0 {
			first = id
		}
	}
	look, err := lookFor(t, c, order, first)
	if err != nil {
		return 0, err
	}
	if err = a.Storefront.ApplyLook(ctx, look); err != nil {
		return 0, fmt.Errorf("templates: look: %w", err)
	}
	for _, p := range draftPolicies() {
		if err = a.Storefront.EnsureDraftPolicy(ctx, p.slug, p.title, p.body); err != nil {
			return 0, fmt.Errorf("templates: policy %s: %w", p.slug, err)
		}
	}
	return t.Version, nil
}

// sample builds a product's input, priced in the store's currency.
func sample(p Product, cur money.Currency, category ulid.ID, pic string) (catalog.SampleProduct, error) {
	price := p.Prices[string(cur)]
	tracked := true
	in := catalog.ProductInput{Slug: p.Slug, Title: p.Title, Description: p.Description, Status: "active", CategoryIDs: []ulid.ID{category}}
	if p.Option == nil {
		in.Variants = []catalog.VariantInput{{PriceMinor: price, Tracked: &tracked, OnHand: p.Stock}}
	} else {
		in.Options = []catalog.Option{{Name: p.Option.Name, Values: p.Option.Values}}
		for _, v := range p.Option.Values {
			in.Variants = append(in.Variants, catalog.VariantInput{OptionValues: []string{v}, PriceMinor: price, Tracked: &tracked, OnHand: p.Stock})
		}
	}
	img, err := picture(pic)
	if err != nil {
		return catalog.SampleProduct{}, err
	}
	return catalog.SampleProduct{Product: in, Image: img, ImageContentType: "image/jpeg", ImageAlt: p.Title}, nil
}

// lookFor is the theme and home page of a look (ADR-0034): classic
// centres the logo over a wide picture and a regular grid; modern puts
// the heading beside a picture over larger cards; soft is rounded, on a
// tint of the colour, with round categories first.
func lookFor(t Template, c domain.Choice, categories []ulid.ID, hero ulid.ID) (storefront.StarterLook, error) {
	l := storefront.StarterLook{
		Colors:       storefront.Colors{Primary: c.Color, Accent: t.Accent, Background: "#ffffff", Text: "#0f172a"},
		Look:         storefront.Look(c.Look),
		Announcement: t.Announcement,
	}
	heroCfg := map[string]any{"heading": t.Hero.Heading, "subheading": t.Hero.Subheading, "button_link": "#s-newest", "align": "center"}
	cats := map[string]any{"category_ids": idStrings(categories)}
	grid := map[string]any{"source": "newest", "limit": 8}
	trust := map[string]any{"items": []map[string]string{{"icon": "cod"}, {"icon": "shipping"}, {"icon": "returns"}}}
	var order []section
	switch c.Look {
	case domain.Classic:
		l.Font, l.Radius = "ibm-plex-sans-arabic", "small"
		heroCfg["image_product_id"] = hero.String()
		order = []section{{"hero", "hero", heroCfg}, {"cats", "category_tiles", cats}, {"newest", "product_grid", grid}, {"trust", "trust_badges", trust}}
	case domain.Modern:
		l.Font, l.Radius, l.Colors.Background = "cairo", "medium", "#f8fafc"
		heroCfg["align"], heroCfg["image_product_id"] = "start", hero.String()
		grid["limit"] = 6
		order = []section{{"hero", "hero", heroCfg}, {"newest", "product_grid", grid}, {"cats", "category_tiles", cats}, {"trust", "trust_badges", trust}}
	case domain.Soft:
		l.Font, l.Radius, l.Colors.Background = "tajawal", "large", tint(c.Color, 0.06)
		order = []section{{"hero", "hero", heroCfg}, {"cats", "category_tiles", cats}, {"newest", "product_grid", grid}, {"trust", "trust_badges", trust}}
	default:
		return storefront.StarterLook{}, fmt.Errorf("templates: unknown look %q", c.Look)
	}
	order = append(order, section{"help", "whatsapp_cta", map[string]any{}})
	for _, s := range order {
		raw, err := json.Marshal(s.settings)
		if err != nil {
			return storefront.StarterLook{}, fmt.Errorf("templates: section %s: %w", s.id, err)
		}
		l.Home = append(l.Home, storefront.Section{ID: s.id, Type: storefront.SectionType(s.kind), Settings: raw})
	}
	return l, nil
}

type section struct {
	id, kind string
	settings map[string]any
}

func idStrings(ids []ulid.ID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}

// tint mixes a #rrggbb colour into white: share 0.06 is a faint wash of
// it, light enough for the body text's contrast.
func tint(color string, share float64) string {
	out := "#"
	for i := 1; i < 7; i += 2 {
		v, err := strconv.ParseUint(color[i:i+2], 16, 8)
		if err != nil {
			return "#ffffff"
		}
		mixed := 255 - share*(255-float64(v))
		out += fmt.Sprintf("%02x", int(mixed+0.5))
	}
	return out
}
