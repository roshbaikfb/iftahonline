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
	if again, err := f.svc.EnsureCategory(ctx, domain.CategoryInput{Slug: "tshirts", Name: "تيشيرتات"}); err != nil || again != cat {
		t.Fatalf("second EnsureCategory = %s, %v; want %s", again, err, cat)
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
	if again, err := f.svc.EnsureSampleProduct(ctx, sampleTee(t, "sample-tee")); err != nil || again != id || f.objects.count() != stored {
		t.Fatalf("second EnsureSampleProduct = %s, %v (objects %d -> %d)", again, err, stored, f.objects.count())
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
	wantKind(t, func() error { _, err := f.svc.GetProduct(ctx, id); return err }(), errs.KindNotFound)
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
