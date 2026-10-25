package app_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/testkit"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

type fakeSigner struct{}

func (fakeSigner) URL(key string, size int, format string) string {
	return fmt.Sprintf("https://img.test/%d/%s.%s", size, key, format)
}

func egp(t *testing.T, minor int64) money.Money {
	t.Helper()
	m, err := money.New(minor, money.EGP)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func newStorefront(f fixture) *app.Storefront { return app.NewStorefront(f.pool, fakeSigner{}) }

// countQueries runs fn and returns how many queries it made.
func countQueries(t *testing.T, f fixture, fn func() error) int64 {
	t.Helper()
	f.qc.Reset()
	if err := fn(); err != nil {
		t.Fatal(err)
	}
	return f.qc.Count()
}

func TestStorefrontProductBySlug(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sf := newStorefront(f)
	ctx := f.ctxA

	p := mustCreate(ctx, t, f, tee("Linen shirt", 1))
	img, _, err := f.svc.CreateImageUpload(ctx, p.ID, "image/jpeg", domain.ImageInput{Alt: "front"})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.objects.Put(ctx, img.UploadKey, "image/jpeg", photo(t, 1000, 500)); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.CompleteImageUpload(ctx, p.ID, img.ID); err != nil {
		t.Fatal(err)
	}
	runJobs(ctx, t, f)
	if _, _, err = f.svc.CreateImageUpload(ctx, p.ID, "image/png", domain.ImageInput{}); err != nil { // stays pending
		t.Fatal(err)
	}

	var got app.StorefrontProduct
	n := countQueries(t, f, func() (e error) { got, e = sf.ProductBySlug(ctx, p.Slug); return e })
	// BEGIN, set tenant, product (with category IDs), variants, images, COMMIT.
	if n != 6 {
		t.Fatalf("ProductBySlug made %d queries, want 6", n)
	}
	if got.ID != p.ID || got.Title != "Linen shirt" || len(got.Variants) != 2 || len(got.Options) != 1 {
		t.Fatalf("product %+v", got)
	}
	if v := got.Variants[1]; v.Price != egp(t, 16000) || v.CompareAt == nil || !v.Available || v.Left != 1 {
		t.Fatalf("variant %+v (1 on hand: Left 1)", v)
	}
	if len(got.Images) != 1 {
		t.Fatalf("images = %d, want only the ready one", len(got.Images))
	}
	im := got.Images[0]
	if im.Alt != "front" || im.Width != 1000 || len(im.Renditions) != 8 {
		t.Fatalf("image %+v", im)
	}
	full, err := f.svc.GetProduct(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	key := full.Images[0].ObjectKey
	if r := im.Renditions[0]; r.Format != "avif" || r.Width != 160 || r.Height != 80 || r.URL != (fakeSigner{}).URL(key, 160, "avif") {
		t.Fatalf("first rendition %+v", r)
	}
	if r := im.Renditions[7]; r.Format != "webp" || r.Width != 1000 || r.Height != 500 {
		t.Fatalf("last rendition %+v (large preset must not enlarge)", r)
	}

	// Not shown: drafts, archived, deleted, other stores, malformed slugs.
	draft := tee("Draft shirt", 1)
	draft.Status = "draft"
	d := mustCreate(ctx, t, f, draft)
	archived := tee("Old shirt", 1)
	archived.Status = "archived"
	a := mustCreate(ctx, t, f, archived)
	gone := mustCreate(ctx, t, f, tee("Gone shirt", 1))
	if err = f.svc.DeleteProduct(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{d.Slug, a.Slug, gone.Slug, "no-such-product"} {
		if _, err = sf.ProductBySlug(ctx, slug); !errors.Is(err, app.ErrProductNotFound) {
			t.Fatalf("slug %q: err = %v", slug, err)
		}
	}
	if _, err = sf.ProductBySlug(f.ctxB, p.Slug); !errors.Is(err, app.ErrProductNotFound) {
		t.Fatalf("store B reads A's product: %v", err)
	}
	for _, slug := range []string{"", "Upper", "-x", "a--b", "<script>"} {
		f.qc.Reset()
		if _, err = sf.ProductBySlug(ctx, slug); !errors.Is(err, app.ErrProductNotFound) || f.qc.Count() != 0 {
			t.Fatalf("malformed slug %q: err %v, %d queries", slug, err, f.qc.Count())
		}
	}
	if _, err = sf.ProductBySlug(t.Context(), p.Slug); !errors.Is(err, tenant.ErrNoTenant) {
		t.Fatalf("no tenant: %v", err)
	}
}

func TestStorefrontProductsByIDs(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sf := newStorefront(f)
	ctx := f.ctxA

	var ids []ulid.ID
	for i := range 30 {
		in := tee(fmt.Sprintf("Pick %02d", i), 1)
		if i%10 == 9 {
			in.Status = "draft"
		}
		ids = append(ids, mustCreate(ctx, t, f, in).ID)
	}
	other := mustCreate(f.ctxB, t, f, tee("Other store", 1))

	var one, many []app.StorefrontProduct
	nOne := countQueries(t, f, func() (e error) { one, e = sf.ProductsByIDs(ctx, ids[:1]); return e })
	// Reversed, with a repeat, another store's product and an unknown ID.
	picked := append([]ulid.ID{ids[3], ids[3], other.ID, ulid.NewGenerator().New()}, ids...)
	for i, j := 4, len(picked)-1; i < j; i, j = i+1, j-1 {
		picked[i], picked[j] = picked[j], picked[i]
	}
	nMany := countQueries(t, f, func() (e error) { many, e = sf.ProductsByIDs(ctx, picked); return e })
	if nOne != 6 || nMany != nOne {
		t.Fatalf("queries: 1 ID = %d, %d IDs = %d; want 6 for both", nOne, len(picked), nMany)
	}
	if len(one) != 1 || one[0].ID != ids[0] {
		t.Fatalf("one = %+v", one)
	}
	// 30 created, 3 drafts; ids[3] comes first, then the rest reversed.
	if len(many) != 27 || many[0].ID != ids[3] || many[1].ID != ids[28] || many[26].ID != ids[0] {
		t.Fatalf("got %d products, first %s, second %s", len(many), many[0].Title, many[1].Title)
	}
	for _, p := range many {
		if p.ID == other.ID {
			t.Fatal("another store's product was returned")
		}
	}

	over := make([]ulid.ID, 0, app.MaxStorefrontIDs+10)
	for range app.MaxStorefrontIDs + 10 {
		over = append(over, ids[0])
	}
	over = append(over, ids[1]) // after 50 repeats of ids[0]: still read, repeats do not count
	if got, err := sf.ProductsByIDs(ctx, over); err != nil || len(got) != 2 {
		t.Fatalf("repeats: %v, %d products", err, len(got))
	}
	if n := countQueries(t, f, func() error { _, e := sf.ProductsByIDs(ctx, nil); return e }); n != 0 {
		t.Fatalf("no IDs made %d queries", n)
	}
}

func TestStorefrontListProducts(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sf := newStorefront(f)
	ctx := f.ctxA
	cat, err := f.svc.CreateCategory(ctx, domain.CategoryInput{Name: "Tops"})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 60 {
		in := tee(fmt.Sprintf("Top %02d", i), 1)
		if i%2 == 0 {
			in.CategoryIDs = []ulid.ID{cat.ID}
		}
		if i%3 == 0 {
			in.Status = "draft"
		}
		mustCreate(ctx, t, f, in)
	}
	mustCreate(f.ctxB, t, f, tee("Top B", 1))

	var page app.StorefrontPage
	n := countQueries(t, f, func() (e error) { page, e = sf.ListProducts(ctx, app.StorefrontQuery{}); return e })
	if n != 6 || len(page.Items) != app.DefaultStorefrontLimit || page.NextCursor == "" {
		t.Fatalf("first page: %d queries, %d items, cursor %q", n, len(page.Items), page.NextCursor)
	}
	if page.Items[0].Title != "Top 59" {
		t.Fatalf("not newest first: %s", page.Items[0].Title)
	}
	total := len(page.Items)
	for page.NextCursor != "" {
		if page, err = sf.ListProducts(ctx, app.StorefrontQuery{Cursor: page.NextCursor, Limit: 1000}); err != nil {
			t.Fatal(err)
		}
		if len(page.Items) > app.MaxStorefrontLimit {
			t.Fatalf("limit not capped: %d", len(page.Items))
		}
		total += len(page.Items)
	}
	if total != 40 { // 60 minus 20 drafts
		t.Fatalf("active products = %d, want 40", total)
	}
	inCat, err := sf.ListProducts(ctx, app.StorefrontQuery{CategoryID: &cat.ID, Limit: 48})
	if err != nil || len(inCat.Items) != 20 { // 30 in the category, 10 of them drafts
		t.Fatalf("category: %v, %d items", err, len(inCat.Items))
	}
	if _, err = sf.ListProducts(ctx, app.StorefrontQuery{Cursor: "bad"}); err == nil {
		t.Fatal("bad cursor accepted")
	}
	b, err := sf.ListProducts(f.ctxB, app.StorefrontQuery{})
	if err != nil || len(b.Items) != 1 || b.Items[0].Title != "Top B" {
		t.Fatalf("store B: %v %+v", err, b.Items)
	}

	cats, err := sf.Categories(ctx)
	if err != nil || len(cats) != 1 || cats[0].ID != cat.ID {
		t.Fatalf("categories: %v %+v", err, cats)
	}
	if cats, err = sf.Categories(f.ctxB); err != nil || len(cats) != 0 {
		t.Fatalf("store B sees A's categories: %v %+v", err, cats)
	}
}

func TestStorefrontProductHelpers(t *testing.T) {
	t.Parallel()
	v := func(price int64, available bool) app.StorefrontVariant {
		return app.StorefrontVariant{ID: ulid.NewGenerator().New(), Price: egp(t, price), Available: available}
	}
	cheapSoldOut, mid, high := v(100, false), v(200, true), v(300, true)
	tests := []struct {
		name      string
		variants  []app.StorefrontVariant
		available bool
		from      app.StorefrontVariant
	}{
		{"cheapest available wins", []app.StorefrontVariant{high, cheapSoldOut, mid}, true, mid},
		{"all sold out: cheapest", []app.StorefrontVariant{v(500, false), cheapSoldOut}, false, cheapSoldOut},
		{"single", []app.StorefrontVariant{high}, true, high},
		{"none", nil, false, app.StorefrontVariant{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := app.StorefrontProduct{Variants: tt.variants}
			if p.Available() != tt.available || p.PriceFrom().ID != tt.from.ID {
				t.Fatalf("available %v, from %+v", p.Available(), p.PriceFrom())
			}
		})
	}
}

func TestFit(t *testing.T) {
	t.Parallel()
	tests := []struct{ w, h, box, wantW, wantH int }{
		{1000, 500, 160, 160, 80},
		{500, 1000, 400, 200, 400},
		{100, 50, 160, 100, 50}, // never enlarged
		{3000, 1, 160, 160, 1},  // never zero
		{0, 0, 160, 0, 0},
	}
	for _, tt := range tests {
		if w, h := app.Fit(tt.w, tt.h, tt.box); w != tt.wantW || h != tt.wantH {
			t.Errorf("Fit(%d, %d, %d) = %d×%d, want %d×%d", tt.w, tt.h, tt.box, w, h, tt.wantW, tt.wantH)
		}
	}
}

// TestSlugLookupUsesIndexUnderRLS: under RLS, PostgreSQL only puts
// LEAKPROOF operators into an index condition. citext's = is not
// leakproof, so a citext slug was filtered row by row across the whole
// store (44 ms on a 200k-product store). The lookup must be an index
// condition as app_user, under a generic plan.
func TestSlugLookupUsesIndexUnderRLS(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	p := mustCreate(f.ctxA, t, f, tee("Indexed", 1))

	pool, capture := testkit.CapturingPool(t, infra.AppURL)
	if _, err := app.NewStorefront(pool, fakeSigner{}).ProductBySlug(f.ctxA, p.Slug); err != nil {
		t.Fatal(err)
	}
	plan := testkit.GenericPlan(f.ctxA, t, f.pool, capture.Last(t, "p.slug = $"))
	testkit.AssertIndexCond(t, plan, "slug")
}

func TestSellableVariants(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.ctxA
	a := mustCreate(ctx, t, f, tee("Sellable", 3))
	draft := tee("Hidden", 3)
	draft.Status = "draft"
	d := mustCreate(ctx, t, f, draft)
	other := mustCreate(f.ctxB, t, f, tee("Other", 3))

	ids := []ulid.ID{a.Variants[1].ID, d.Variants[0].ID, other.Variants[0].ID, f.ids.New(), a.Variants[0].ID}
	var got []domain.SellableVariant
	n := countQueries(t, f, func() (e error) { got, e = f.svc.SellableVariants(ctx, ids); return e })
	if n != 4 { // BEGIN, tenant, one lookup for all, COMMIT
		t.Fatalf("%d queries for %d IDs, want 4", n, len(ids))
	}
	if len(got) != 2 || got[0].VariantID != a.Variants[1].ID || got[1].VariantID != a.Variants[0].ID ||
		got[0].ProductTitle != "Sellable" || got[0].Price != egp(t, 16000) || got[0].Stock.OnHand != 3 {
		t.Fatalf("got %+v", got)
	}
	if got[0].ImageKey != "" {
		t.Fatalf("a product without a ready image has image %q", got[0].ImageKey)
	}

	// A cart's thumbnails come with the same single lookup: the first
	// ready picture by position.
	img, _, err := f.svc.CreateImageUpload(ctx, a.ID, "image/jpeg", domain.ImageInput{})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.objects.Put(ctx, img.UploadKey, "image/jpeg", photo(t, 400, 400)); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.CompleteImageUpload(ctx, a.ID, img.ID); err != nil {
		t.Fatal(err)
	}
	runJobs(ctx, t, f)
	n = countQueries(t, f, func() (e error) { got, e = f.svc.SellableVariants(ctx, ids); return e })
	full, err := f.svc.GetProduct(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 || len(got) != 2 || got[0].ImageKey == "" || got[0].ImageKey != full.Images[0].ObjectKey || got[1].ImageKey != got[0].ImageKey {
		t.Fatalf("%d queries; images %q %q, want %q", n, got[0].ImageKey, got[1].ImageKey, full.Images[0].ObjectKey)
	}
	if empty, err := f.svc.SellableVariants(ctx, nil); err != nil || len(empty) != 0 {
		t.Fatalf("no IDs: %v %v", empty, err)
	}
	many := make([]ulid.ID, app.MaxSellableIDs+1)
	if _, err := f.svc.SellableVariants(ctx, many); err == nil {
		t.Fatal("too many IDs accepted")
	}
}
