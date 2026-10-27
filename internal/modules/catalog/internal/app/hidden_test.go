package app_test

import (
	"errors"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// keepIDs is in for an update: every variant keeps its ID (and stock).
func keepIDs(in domain.ProductInput, p domain.Product) domain.ProductInput {
	for i := range in.Variants {
		in.Variants[i].ID = &p.Variants[i].ID
	}
	return in
}

// TestHiddenVariant (ADR-0035): the merchant takes a variant off sale; it
// keeps its stock, cannot be bought or reserved, and shoppers do not see
// it; shown again, it sells with the stock it had.
func TestHiddenVariant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sf := newStorefront(f)
	ctx := f.ctxA
	p := mustCreate(ctx, t, f, tee("Tee", 6))
	small := p.Variants[0]

	in := keepIDs(tee("Tee", 6), p)
	in.Variants[0].Hidden = true
	p, err := f.svc.UpdateProduct(ctx, p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Variants[0].Hidden || p.Variants[0].ID != small.ID || p.Variants[0].Stock.OnHand != 6 || p.Variants[1].Hidden {
		t.Fatalf("variants after hiding: %+v", p.Variants)
	}

	_, err = f.svc.GetSellableVariant(ctx, small.ID)
	wantKind(t, err, errs.KindNotFound)
	if got, _ := f.svc.SellableVariants(ctx, []ulid.ID{small.ID, p.Variants[1].ID}); len(got) != 1 || got[0].VariantID != p.Variants[1].ID {
		t.Fatalf("sellable = %+v", got)
	}
	_, err = f.svc.ReserveStock(ctx, domain.ReserveRequest{Ref: "h", Lines: []domain.ReserveLine{{VariantID: small.ID, Qty: 1}}})
	if !errors.Is(err, app.ErrVariantNotSellable) {
		t.Fatalf("reserve hidden: %v", err)
	}
	// running low lists the shown variant (6 left), not the hidden one
	low, total, err := f.svc.LowStock(ctx, 10, 20)
	if err != nil || total != 1 || low[0].VariantID != p.Variants[1].ID {
		t.Fatalf("low stock = %+v (%d), %v", low, total, err)
	}
	shop, err := sf.ProductBySlug(ctx, p.Slug)
	if err != nil || len(shop.Variants) != 1 || shop.Variants[0].ID != p.Variants[1].ID {
		t.Fatalf("shop variants = %+v, %v", shop.Variants, err)
	}

	// every variant hidden: the shop has nothing to sell or price
	in.Variants[1].Hidden = true
	if _, err = f.svc.UpdateProduct(ctx, p.ID, in); err != nil {
		t.Fatal(err)
	}
	_, err = sf.ProductBySlug(ctx, p.Slug)
	wantKind(t, err, errs.KindNotFound)
	page, err := sf.ListProducts(ctx, app.StorefrontQuery{})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("listed a product with every variant hidden: %d %v", len(page.Items), err)
	}

	// shown again, with the stock it had
	in.Variants[0].Hidden, in.Variants[1].Hidden = false, false
	if p, err = f.svc.UpdateProduct(ctx, p.ID, in); err != nil {
		t.Fatal(err)
	}
	sv, err := f.svc.GetSellableVariant(ctx, small.ID)
	if err != nil || sv.Stock.OnHand != 6 {
		t.Fatalf("shown again: %+v %v", sv, err)
	}

	// a new variant may start hidden
	created := tee("Cap", 2)
	created.Variants[1].Hidden = true
	if c := mustCreate(ctx, t, f, created); !c.Variants[1].Hidden || c.Variants[0].Hidden {
		t.Fatalf("created hidden = %+v", c.Variants)
	}
}
