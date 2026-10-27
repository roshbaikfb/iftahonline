package app_test

import (
	"slices"
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

func titles(ps []domain.Product) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Title
	}
	slices.Sort(out)
	return out
}

// TestListProductsByStock: in_stock has a variant that can be bought,
// out has none, low has a tracked variant of 1 to the threshold free.
func TestListProductsByStock(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.ctxA
	mustCreate(ctx, t, f, single("plenty", 50, true))
	mustCreate(ctx, t, f, single("few", 3, true))
	mustCreate(ctx, t, f, single("none", 0, true))
	mustCreate(ctx, t, f, single("untracked", 0, false))
	mixed := tee("mixed", 0) // S and M sold out …
	mixed.Variants[1].OnHand = 7
	mustCreate(ctx, t, f, mixed) // … M has 7
	mustCreate(f.ctxB, t, f, single("B few", 2, true))

	tests := []struct {
		stock string
		low   int
		want  []string
	}{
		{"", 0, []string{"few", "mixed", "none", "plenty", "untracked"}},
		{domain.StockInStock, 0, []string{"few", "mixed", "plenty", "untracked"}},
		{domain.StockOut, 0, []string{"none"}},
		{domain.StockLow, 0, []string{"few"}},
		{domain.StockLow, 7, []string{"few", "mixed"}},
	}
	for _, tt := range tests {
		page, err := f.svc.ListProducts(ctx, app.ProductQuery{Stock: tt.stock, LowThreshold: tt.low})
		if err != nil {
			t.Fatal(err)
		}
		if got := titles(page.Items); !slices.Equal(got, tt.want) {
			t.Errorf("stock %q low %d = %q, want %q", tt.stock, tt.low, got, tt.want)
		}
	}
	// with a category too (the category's own query)
	cat, err := f.svc.CreateCategory(ctx, domain.CategoryInput{Name: "Bags"})
	if err != nil {
		t.Fatal(err)
	}
	in := single("bag", 0, true)
	in.CategoryIDs = []ulid.ID{cat.ID}
	mustCreate(ctx, t, f, in)
	page, err := f.svc.ListProducts(ctx, app.ProductQuery{CategoryID: &cat.ID, Stock: domain.StockOut})
	if err != nil || !slices.Equal(titles(page.Items), []string{"bag"}) {
		t.Fatalf("out in category = %q, %v", titles(page.Items), err)
	}

	_, err = f.svc.ListProducts(ctx, app.ProductQuery{Stock: "some"})
	wantField(t, err, "stock", "invalid")
	_, err = f.svc.ListProducts(ctx, app.ProductQuery{LowThreshold: domain.MaxLowThreshold + 1})
	wantField(t, err, "low_threshold", "range")
}

func TestCountProducts(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.ctxA
	mustCreate(ctx, t, f, single("a", 1, true))
	mustCreate(ctx, t, f, single("b", 1, true))
	draft := single("c", 1, true)
	draft.Status = "draft"
	mustCreate(ctx, t, f, draft)
	gone := mustCreate(ctx, t, f, single("d", 1, true))
	if err := f.svc.DeleteProduct(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	mustCreate(f.ctxB, t, f, single("B", 1, true))
	c, err := f.svc.CountProducts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if c != (app.ProductCounts{All: 3, Active: 2, Draft: 1}) {
		t.Fatalf("counts = %+v", c)
	}
}

func TestBulkProducts(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.ctxA
	a := mustCreate(ctx, t, f, single("a", 1, true))
	b := mustCreate(ctx, t, f, single("b", 1, true))
	keep := mustCreate(ctx, t, f, single("keep", 1, true))
	other := mustCreate(f.ctxB, t, f, single("B", 1, true))
	f.events.take()

	n, err := f.svc.BulkProducts(ctx, []ulid.ID{a.ID, b.ID, other.ID, ulid.NewGenerator().New()}, "archived")
	if err != nil || n != 2 {
		t.Fatalf("archive = %d, %v", n, err)
	}
	got := f.events.take()
	slices.Sort(got)
	want := []string{changed(a.ID, domain.ReasonUpdated), changed(b.ID, domain.ReasonUpdated)}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("events = %q, want %q", got, want)
	}
	for id, st := range map[ulid.ID]domain.ProductStatus{a.ID: domain.StatusArchived, b.ID: domain.StatusArchived, keep.ID: domain.StatusActive} {
		if p, err := f.svc.GetProduct(ctx, id); err != nil || p.Status != st {
			t.Fatalf("%s status = %q, %v", id, p.Status, err)
		}
	}
	if p, err := f.svc.GetProduct(f.ctxB, other.ID); err != nil || p.Status != domain.StatusActive {
		t.Fatalf("store B's product changed: %q %v", p.Status, err)
	}

	if n, err = f.svc.BulkProducts(ctx, []ulid.ID{a.ID, a.ID, other.ID}, domain.BulkDelete); err != nil || n != 1 {
		t.Fatalf("delete = %d, %v", n, err)
	}
	_, err = f.svc.GetProduct(ctx, a.ID)
	wantKind(t, err, errs.KindNotFound)
	if _, err = f.svc.GetProduct(f.ctxB, other.ID); err != nil {
		t.Fatalf("store B's product deleted: %v", err)
	}

	_, err = f.svc.BulkProducts(ctx, []ulid.ID{b.ID}, "publish")
	wantField(t, err, "action", "invalid")
	_, err = f.svc.BulkProducts(ctx, nil, "draft")
	wantField(t, err, "product_ids", "count")
	_, err = f.svc.BulkProducts(ctx, make([]ulid.ID, domain.MaxBulkProducts+1), "draft")
	wantField(t, err, "product_ids", "count")
}
