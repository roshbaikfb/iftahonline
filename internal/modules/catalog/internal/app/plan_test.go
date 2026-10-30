package app_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

// TestProductsWithinThePlan: a store holds what its plan's products
// grant; deleting one makes room; another store counts apart (ADR-0045).
func TestProductsWithinThePlan(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ent, err := entitlements.New(entitlements.Deps{Pool: f.pool, Source: entitlements.Fixed{entitlements.Products: {Value: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	d := f.deps
	d.Plan = ent.Entitlements()
	svc := app.NewService(d)

	first, err := svc.CreateProduct(f.ctxA, tee("One", 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.CreateProduct(f.ctxA, tee("Two", 1)); err != nil {
		t.Fatal(err)
	}
	_, err = svc.CreateProduct(f.ctxA, tee("Three", 1))
	var e *errs.Error
	if !errors.Is(err, entitlements.ErrLimitReached) || !errors.As(err, &e) || e.Kind != errs.KindForbidden || e.Code != entitlements.PlanLimitCode {
		t.Fatalf("the third product: %v", err)
	}
	if _, err = svc.CreateProduct(f.ctxB, tee("Other", 1)); err != nil {
		t.Fatalf("store B counts apart: %v", err)
	}
	if err = svc.DeleteProduct(f.ctxA, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.CreateProduct(f.ctxA, tee("Three", 1)); err != nil {
		t.Fatalf("after a deletion: %v", err)
	}
}

// TestImportStopsAtThePlan: an import creates what the plan allows; each
// row past it fails with a message the merchant can act on, and rows
// that update existing products still go through.
func TestImportStopsAtThePlan(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ent, err := entitlements.New(entitlements.Deps{Pool: f.pool, Source: entitlements.Fixed{entitlements.Products: {Value: 3}}})
	if err != nil {
		t.Fatal(err)
	}
	d := f.deps
	d.Plan = ent.Entitlements()
	f.svc = app.NewService(d)

	im := fileImport(f.ctxA, t, f, "handle,title,price\na,A,10\nb,B,10\nc,C,10\nd,D,10\ne,E,10\n")
	if im.Created != 3 || im.Failed != 2 || len(im.RowErrors) != 2 || !strings.Contains(im.RowErrors[0].Message, "upgrade the plan") {
		t.Fatalf("import = %d created, %d failed, %+v", im.Created, im.Failed, im.RowErrors)
	}
	again := fileImport(f.ctxA, t, f, "handle,title,price\na,A again,12\n")
	if again.Failed != 0 || again.Updated != 1 {
		t.Fatalf("an update at the limit = %+v", again)
	}
	if n := countProducts(f.ctxA, t, f); n != 3 {
		t.Fatalf("%d products, want 3", n)
	}
}
