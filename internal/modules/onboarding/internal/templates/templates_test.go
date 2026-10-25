package templates_test

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/onboarding/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/onboarding/internal/templates"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront"
	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testinfra"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var infra testinfra.Infra

func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }

func TestEveryTemplateLoads(t *testing.T) {
	t.Parallel()
	ts, err := templates.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != len(domain.Activities()) {
		t.Fatalf("%d templates", len(ts))
	}
	for a, tpl := range ts {
		if len(tpl.Categories) != 3 || len(tpl.Products) != 6 {
			t.Errorf("%s: %d categories, %d products", a, len(tpl.Categories), len(tpl.Products))
		}
	}
}

func TestParseRefusesBrokenTemplates(t *testing.T) {
	t.Parallel()
	good, err := os.ReadFile("data/clothing.json")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"unknown field":    strings.Replace(string(good), `"version"`, `"colour": 1, "version"`, 1),
		"trailing data":    string(good) + "{}",
		"unknown category": strings.Replace(string(good), `"category": "pants"`, `"category": "hats"`, 1),
		"missing picture":  strings.Replace(string(good), `"image": "clothing-1"`, `"image": "nope"`, 1),
		"slug not sample-": strings.Replace(string(good), `"sample-basic-tee"`, `"basic-tee"`, 1),
		"no price":         strings.Replace(string(good), `"SAR": 4900`, `"SAR": 0`, 1),
	}
	for name, data := range cases {
		if _, err := templates.Parse([]byte(data)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, a := range domain.Activities() {
		if b, err := os.ReadFile("data/" + string(a) + ".json"); err == nil {
			f.Add(b)
		}
	}
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		tpl, err := templates.Parse(data)
		if err != nil {
			return
		}
		// What parses is whole: every product's category exists.
		cats := map[string]bool{}
		for _, c := range tpl.Categories {
			cats[c.Slug] = true
		}
		for _, p := range tpl.Products {
			if !cats[p.Category] {
				t.Fatalf("accepted a product in unknown category %q", p.Category)
			}
		}
	})
}

// fakeCatalog keeps what the template adds, by slug, like the real one.
type fakeCatalog struct {
	mu       sync.Mutex
	cats     map[string]ulid.ID
	products map[string]catalog.SampleProduct
	ids      map[string]ulid.ID
	gen      *ulid.Generator
}

func (c *fakeCatalog) EnsureCategory(_ context.Context, in catalog.CategoryInput) (ulid.ID, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id, ok := c.cats[in.Slug]; ok {
		return id, nil
	}
	c.cats[in.Slug] = c.gen.New()
	return c.cats[in.Slug], nil
}

func (c *fakeCatalog) EnsureSampleProduct(_ context.Context, in catalog.SampleProduct) (ulid.ID, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id, ok := c.ids[in.Product.Slug]; ok {
		return id, nil
	}
	c.products[in.Product.Slug], c.ids[in.Product.Slug] = in, c.gen.New()
	return c.ids[in.Product.Slug], nil
}

// The template's home page goes through the real storefront module, so
// every section is checked against its schema.
func TestApply(t *testing.T) {
	t.Parallel()
	pool, err := pgxpool.New(t.Context(), infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ins, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Middleware: jobs.Middleware()})
	if err != nil {
		t.Fatal(err)
	}
	media := config.Media{S3Endpoint: "http://127.0.0.1:1", S3PublicEndpoint: "http://127.0.0.1:1", S3Region: "us-east-1", S3Bucket: "b",
		ImgproxyURL: "https://img.test", ImgproxyKey: []byte("0123456789abcdef"), ImgproxySalt: []byte("salt-0123456789a"),
		MaxUploadBytes: 1 << 20, MaxImagePixels: 1 << 20} // nothing here is contacted
	ids := ulid.NewGenerator()
	shop, err := storefront.New(storefront.Deps{Pool: pool, IDs: ids, Events: eventbus.Nop{}, Jobs: ins, Media: media})
	if err != nil {
		t.Fatal(err)
	}

	for _, look := range domain.Looks() {
		t.Run(string(look), func(t *testing.T) {
			id := tenant.StoreID{ID: ids.New()}
			ctx := tenant.WithStore(t.Context(), id)
			if txErr := postgres.InTenantTx(ctx, pool, func(tx pgx.Tx) error {
				_, execErr := tx.Exec(ctx, `INSERT INTO stores (id, slug, name, currency, locale, timezone) VALUES ($1, $2, 'Test', 'SAR', 'ar', 'Asia/Riyadh')`,
					id.ID, "tpl-"+strings.ToLower(id.String()[14:]))
				return execErr
			}); txErr != nil {
				t.Fatal(txErr)
			}
			cat := &fakeCatalog{cats: map[string]ulid.ID{}, products: map[string]catalog.SampleProduct{}, ids: map[string]ulid.ID{}, gen: ids}
			a, newErr := templates.NewApplier(cat, shop.Starter(), func(context.Context) (money.Currency, error) { return money.SAR, nil })
			if newErr != nil {
				t.Fatal(newErr)
			}
			choice := domain.Choice{Activity: domain.Clothing, Look: look, Color: "#1e3a5f"}
			for range 2 { // a job may run twice
				v, applyErr := a.Apply(ctx, choice)
				if applyErr != nil || v != 1 {
					t.Fatalf("Apply = %d, %v", v, applyErr)
				}
			}
			if len(cat.cats) != 3 || len(cat.products) != 6 {
				t.Fatalf("%d categories, %d products", len(cat.cats), len(cat.products))
			}
			tee := cat.products["sample-basic-tee"]
			if len(tee.Product.Variants) != 4 || tee.Product.Variants[0].PriceMinor != 4900 || tee.Image == nil ||
				tee.ImageContentType != "image/jpeg" || tee.Product.CategoryIDs[0] != cat.cats["tshirts"] {
				t.Fatalf("tee = %+v", tee.Product)
			}
			home := homeOf(ctx, t, pool)
			if !strings.Contains(home, cat.cats["tshirts"].String()) || !strings.Contains(home, `"newest"`) {
				t.Fatalf("home = %s", home)
			}
			if look == domain.Soft && strings.Contains(home, "image_product_id") {
				t.Fatal("the soft look's hero has a picture")
			}
			if look != domain.Soft && !strings.Contains(home, cat.ids["sample-basic-tee"].String()) {
				t.Fatalf("the hero does not show the first product: %s", home)
			}
		})
	}

	ctx := tenant.WithStore(t.Context(), tenant.StoreID{ID: ids.New()})
	a, err := templates.NewApplier(&fakeCatalog{}, shop.Starter(), func(context.Context) (money.Currency, error) { return money.EGP, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Apply(ctx, domain.Choice{Activity: "cars", Look: domain.Soft, Color: "#000000"}); err == nil {
		t.Fatal("an unknown activity was applied")
	}
}

// homeOf reads the saved home page's sections.
func homeOf(ctx context.Context, t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var sections []byte
	if err := postgres.InTenantTx(ctx, pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT sections FROM store_pages WHERE kind = 'home'`).Scan(&sections)
	}); err != nil {
		t.Fatal(err)
	}
	return string(sections)
}
