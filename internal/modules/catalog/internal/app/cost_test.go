package app_test

import (
	"errors"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// TestVariantCost (ADR-0068): the merchant's cost is saved per variant,
// kept when an update sends none (imports), given to orders with the
// sellable variant, and never to shoppers.
func TestVariantCost(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sf := newStorefront(f)
	ctx := f.ctxA
	cost := int64(4200)
	in := tee("Tee", 6)
	in.Variants[0].CostMinor = &cost
	p := mustCreate(ctx, t, f, in)
	if c := p.Variants[0].Cost; c == nil || c.Amount() != 4200 || p.Variants[1].Cost != nil {
		t.Fatalf("created: %+v / %+v", p.Variants[0].Cost, p.Variants[1].Cost)
	}

	// An update without costs keeps them; one with a cost changes it.
	up := keepIDs(tee("Tee", 6), p)
	p, err := f.svc.UpdateProduct(ctx, p.ID, up)
	if err != nil || p.Variants[0].Cost == nil || p.Variants[0].Cost.Amount() != 4200 {
		t.Fatalf("after an update without costs: %+v, %v", p.Variants[0].Cost, err)
	}
	zero := int64(0)
	up.Variants[1].CostMinor = &zero // free to the merchant is a cost too
	if p, err = f.svc.UpdateProduct(ctx, p.ID, up); err != nil || p.Variants[1].Cost == nil || !p.Variants[1].Cost.IsZero() {
		t.Fatalf("a zero cost: %+v, %v", p.Variants[1].Cost, err)
	}

	got, err := f.svc.SellableVariants(ctx, []ulid.ID{p.Variants[0].ID})
	if err != nil || len(got) != 1 || got[0].Cost == nil || got[0].Cost.Amount() != 4200 {
		t.Fatalf("sellable = %+v, %v", got, err)
	}
	one, err := f.svc.GetSellableVariant(ctx, p.Variants[0].ID)
	if err != nil || one.Cost == nil || one.Cost.Amount() != 4200 {
		t.Fatalf("sellable variant = %+v, %v", one, err)
	}
	if _, err = sf.ProductBySlug(ctx, p.Slug); err != nil { // the shop's view has no cost field at all
		t.Fatal(err)
	}

	bad := int64(-1)
	up.Variants[0].CostMinor = &bad
	_, err = f.svc.UpdateProduct(ctx, p.ID, up)
	wantKind(t, err, errs.KindValidation)
	var e *errs.Error
	if !errors.As(err, &e) || len(e.Fields) != 1 || e.Fields[0].Field != "variants[0].cost_minor" {
		t.Fatalf("err = %+v", err)
	}
}
