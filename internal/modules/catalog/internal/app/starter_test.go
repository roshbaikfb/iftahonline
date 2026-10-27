package app_test

import (
	"errors"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// count is how many objects are stored.
func (m *memStore) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.objects)
}

func sampleTee(t *testing.T, slug string) app.SampleProduct {
	t.Helper()
	p := tee("تيشيرت قطن", 10)
	p.Slug = slug
	return app.SampleProduct{Product: p, Image: photo(t, 40, 40), ImageContentType: "image/jpeg", ImageAlt: "تيشيرت"}
}

// The starter template's catalog part (ADR-0034): idempotent by slug,
// samples shown but never sellable, deleted in one go.
func TestStarterSamples(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.ctxA

	cat, err := f.svc.EnsureCategory(ctx, domain.CategoryInput{Slug: "tshirts", Name: "تيشيرتات"})
	if err != nil {
		t.Fatal(err)
	}
	if again, againErr := f.svc.EnsureCategory(ctx, domain.CategoryInput{Slug: "tshirts", Name: "تيشيرتات"}); againErr != nil || again != cat {
		t.Fatalf("second EnsureCategory = %s, %v; want %s", again, againErr, cat)
	}

	in := sampleTee(t, "sample-tee")
	in.Product.CategoryIDs = []ulid.ID{cat}
	id, err := f.svc.EnsureSampleProduct(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	runJobs(ctx, t, f)
	p, err := f.svc.GetProduct(ctx, id)
	if err != nil || !p.Sample || len(p.Images) != 1 || p.Images[0].Status != domain.ImageReady || p.Images[0].Alt != "تيشيرت" ||
		len(p.CategoryIDs) != 1 || p.CategoryIDs[0] != cat {
		t.Fatalf("sample = %+v, %v", p, err)
	}
	// Running the template again adds nothing and stores no picture.
	stored := f.objects.count()
	if again, againErr := f.svc.EnsureSampleProduct(ctx, sampleTee(t, "sample-tee")); againErr != nil || again != id || f.objects.count() != stored {
		t.Fatalf("second EnsureSampleProduct = %s, %v (objects %d -> %d)", again, againErr, stored, f.objects.count())
	}

	// Shown but never sold.
	if _, err = f.svc.GetSellableVariant(ctx, p.Variants[0].ID); err == nil {
		t.Fatal("a sample's variant is sellable")
	}
	_, err = f.svc.ReserveStock(ctx, domain.ReserveRequest{Ref: "order-1", Lines: []domain.ReserveLine{{VariantID: p.Variants[0].ID, Qty: 1}}})
	if !errors.Is(err, app.ErrVariantNotSellable) {
		t.Fatalf("reserving a sample = %v; want ErrVariantNotSellable", err)
	}

	// A merchant's own product keeps its slug from the template.
	own := mustCreate(ctx, t, f, single("منتجي", 1, true))
	kept, err := f.svc.EnsureSampleProduct(ctx, app.SampleProduct{Product: domain.ProductInput{Slug: own.Slug, Title: "x", Status: "active",
		Variants: []domain.VariantInput{{PriceMinor: 100}}}})
	if err != nil || kept != own.ID {
		t.Fatalf("EnsureSampleProduct over the merchant's slug = %s, %v", kept, err)
	}
	if got, _ := f.svc.GetProduct(ctx, own.ID); got.Sample || got.Title != "منتجي" {
		t.Fatalf("the merchant's product changed: %+v", got)
	}

	// Store B's samples are B's.
	if _, err = f.svc.EnsureSampleProduct(f.ctxB, app.SampleProduct{Product: domain.ProductInput{Slug: "sample-b", Title: "B", Status: "active",
		Variants: []domain.VariantInput{{PriceMinor: 100}}}}); err != nil {
		t.Fatal(err)
	}

	n, err := f.svc.DeleteSamples(ctx)
	if err != nil || n != 1 {
		t.Fatalf("DeleteSamples = %d, %v", n, err)
	}
	_, gone := f.svc.GetProduct(ctx, id)
	wantKind(t, gone, errs.KindNotFound)
	if _, err = f.svc.GetProduct(ctx, own.ID); err != nil {
		t.Fatalf("the merchant's product was deleted: %v", err)
	}
	if n, err = f.svc.DeleteSamples(ctx); err != nil || n != 0 {
		t.Fatalf("DeleteSamples again = %d, %v", n, err)
	}
	if n, err = f.svc.DeleteSamples(f.ctxB); err != nil || n != 1 {
		t.Fatalf("store B's samples = %d, %v", n, err)
	}
}

func TestSampleValidation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	bad := sampleTee(t, "bad")
	bad.ImageContentType = "image/svg+xml"
	if _, err := f.svc.EnsureSampleProduct(f.ctxA, bad); errs.KindOf(err) != errs.KindValidation {
		t.Fatalf("an SVG picture = %v", err)
	}
	if f.objects.count() != 0 {
		t.Fatal("a refused picture was stored")
	}
	if _, err := f.svc.EnsureCategory(f.ctxA, domain.CategoryInput{}); errs.KindOf(err) != errs.KindValidation {
		t.Fatalf("an empty category = %v", err)
	}
}

// The dashboard home's "running low": tracked variants of active,
// non-sample products, by units free to sell.
func TestLowStock(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.ctxA
	shirt := mustCreate(ctx, t, f, tee("تيشيرت", 10)) // two variants, 10 each
	low := mustCreate(ctx, t, f, single("كاب", 2, true))
	mustCreate(ctx, t, f, single("بلا مخزون", 0, false)) // untracked: never low
	draft := single("مسودة", 1, true)
	draft.Status = "draft"
	mustCreate(ctx, t, f, draft)
	sample := app.SampleProduct{Product: single("عينة", 1, true)}
	sample.Product.Slug = "sample-low"
	if _, err := f.svc.EnsureSampleProduct(ctx, sample); err != nil {
		t.Fatal(err)
	}

	items, total, err := f.svc.LowStock(ctx, 5, 5)
	if err != nil || total != 1 || len(items) != 1 || items[0].ProductID != low.ID || items[0].Available != 2 || items[0].ProductTitle != "كاب" {
		t.Fatalf("LowStock = %+v (%d), %v", items, total, err)
	}
	// Reserving 9 of a shirt's 10 leaves 1: it comes first.
	if _, err = f.svc.ReserveStock(ctx, domain.ReserveRequest{Ref: "o-1", Lines: []domain.ReserveLine{{VariantID: shirt.Variants[0].ID, Qty: 9}}}); err != nil {
		t.Fatal(err)
	}
	items, total, err = f.svc.LowStock(ctx, 5, 5)
	if err != nil || total != 2 || items[0].VariantID != shirt.Variants[0].ID || items[0].Available != 1 || len(items[0].OptionValues) != 1 {
		t.Fatalf("after a reservation = %+v (%d), %v", items, total, err)
	}
	if items, total, err = f.svc.LowStock(ctx, 5, 1); err != nil || len(items) != 1 || total != 2 {
		t.Fatalf("limit 1 = %d items of %d, %v", len(items), total, err)
	}
	if _, _, err = f.svc.LowStock(f.ctxB, 5, 5); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][2]int{{-1, 5}, {101, 5}, {5, 0}, {5, 21}} {
		if _, _, err = f.svc.LowStock(ctx, c[0], c[1]); errs.KindOf(err) != errs.KindValidation {
			t.Errorf("threshold %d limit %d = %v", c[0], c[1], err)
		}
	}
}
