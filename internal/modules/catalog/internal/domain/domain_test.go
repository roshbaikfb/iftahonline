package domain_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

func ptr[T any](v T) *T { return &v }

// fieldCodes returns "field:code" for every field error in err.
func fieldCodes(t *testing.T, err error) []string {
	t.Helper()
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindValidation {
		t.Fatalf("want a validation error, got %v", err)
	}
	out := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		out[i] = f.Field + ":" + f.Code
	}
	return out
}

func validInput() domain.ProductInput {
	return domain.ProductInput{
		Title: "  Basic Tee ",
		Options: []domain.Option{
			{Name: "Size", Values: []string{"S", "M"}},
			{Name: "Color", Values: []string{"Red"}},
		},
		Variants: []domain.VariantInput{
			{SKU: "TEE-S-R", OptionValues: []string{"s", " red"}, PriceMinor: 15000, CompareAtMinor: ptr(int64(20000)), OnHand: 5},
			{OptionValues: []string{"M", "Red"}, PriceMinor: 15000},
		},
	}
}

func TestValidateProductNormalizes(t *testing.T) {
	t.Parallel()
	out, err := domain.ValidateProduct(validInput())
	if err != nil {
		t.Fatal(err)
	}
	if out.Title != "Basic Tee" || out.Slug != "basic-tee" || out.Status != "draft" {
		t.Fatalf("normalized = %q %q %q", out.Title, out.Slug, out.Status)
	}
	if got := out.Variants[0].OptionValues; !slices.Equal(got, []string{"S", "Red"}) {
		t.Fatalf("option values not canonical: %q", got)
	}
	if !out.Variants[0].IsTracked() {
		t.Fatal("variants track stock by default")
	}
	if out.Variants[1].IsTracked() != true || (domain.VariantInput{Tracked: ptr(false)}).IsTracked() {
		t.Fatal("IsTracked")
	}
}

func TestValidateProductRules(t *testing.T) {
	t.Parallel()
	id := ulid.NewGenerator().New()
	tests := []struct {
		name   string
		mutate func(*domain.ProductInput)
		want   []string
	}{
		{"empty title", func(p *domain.ProductInput) { p.Title = " " }, []string{"title:length", "slug:required"}},
		{"symbol title needs slug", func(p *domain.ProductInput) { p.Title = "!!!" }, []string{"slug:required"}},
		{"bad slug", func(p *domain.ProductInput) { p.Slug = "a--b" }, []string{"slug:invalid"}},
		{"control char", func(p *domain.ProductInput) { p.Title = "a\x07b" }, []string{"title:invalid"}},
		{"long description", func(p *domain.ProductInput) { p.Description = strings.Repeat("x", domain.MaxDescriptionLen+1) }, []string{"description:length"}},
		{"status", func(p *domain.ProductInput) { p.Status = "live" }, []string{"status:invalid"}},
		{"dup categories", func(p *domain.ProductInput) { p.CategoryIDs = []ulid.ID{id, id} }, []string{"category_ids:duplicate"}},
		{"too many categories", func(p *domain.ProductInput) { p.CategoryIDs = make([]ulid.ID, domain.MaxProductCategories+1) }, []string{"category_ids:too_many"}},
		{"too many options", func(p *domain.ProductInput) {
			p.Options = append(p.Options, domain.Option{Name: "A", Values: []string{"1"}}, domain.Option{Name: "B", Values: []string{"1"}})
		}, []string{"options:too_many"}},
		{"dup option name", func(p *domain.ProductInput) { p.Options[1].Name = "size" }, []string{"options[1].name:duplicate"}},
		{"no option values", func(p *domain.ProductInput) { p.Options[1].Values = nil }, []string{"options[1].values:count"}},
		{"dup option value", func(p *domain.ProductInput) { p.Options[0].Values = []string{"S", "s"} }, []string{"options[0].values[1]:duplicate"}},
		{"empty option value", func(p *domain.ProductInput) { p.Options[0].Values = []string{"S", " "} }, []string{"options[0].values[1]:length"}},
		{"no variants", func(p *domain.ProductInput) { p.Variants = nil }, []string{"variants:required"}},
		{"too many variants", func(p *domain.ProductInput) { p.Variants = make([]domain.VariantInput, domain.MaxVariants+1) }, []string{"variants:too_many"}},
		{"no options, two variants", func(p *domain.ProductInput) {
			p.Options = nil
			p.Variants = []domain.VariantInput{{}, {}}
		}, []string{"variants:too_many"}},
		{"wrong value count", func(p *domain.ProductInput) { p.Variants[1].OptionValues = []string{"M"} }, []string{"variants[1].option_values:count"}},
		{"unknown value", func(p *domain.ProductInput) { p.Variants[1].OptionValues = []string{"XL", "Red"} }, []string{"variants[1].option_values[0]:unknown"}},
		{"dup combo", func(p *domain.ProductInput) { p.Variants[1].OptionValues = []string{"S", "red"} }, []string{"variants[1].option_values:duplicate"}},
		{"dup sku", func(p *domain.ProductInput) { p.Variants[1].SKU = "tee-s-r" }, []string{"variants[1].sku:duplicate"}},
		{"long sku", func(p *domain.ProductInput) { p.Variants[1].SKU = strings.Repeat("k", 65) }, []string{"variants[1].sku:length"}},
		{"dup id", func(p *domain.ProductInput) { p.Variants[0].ID, p.Variants[1].ID = &id, &id }, []string{"variants[1].id:duplicate"}},
		{"negative price", func(p *domain.ProductInput) { p.Variants[1].PriceMinor = -1 }, []string{"variants[1].price_minor:range"}},
		{"huge price", func(p *domain.ProductInput) { p.Variants[1].PriceMinor = domain.MaxPriceMinor + 1 }, []string{"variants[1].price_minor:range"}},
		{"compare-at not above price", func(p *domain.ProductInput) { p.Variants[0].CompareAtMinor = ptr(int64(15000)) }, []string{"variants[0].compare_at_minor:range"}},
		{"negative stock", func(p *domain.ProductInput) { p.Variants[0].OnHand = -1 }, []string{"variants[0].on_hand:range"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in := validInput()
			tt.mutate(&in)
			_, err := domain.ValidateProduct(in)
			if got := fieldCodes(t, err); !slices.Equal(got, tt.want) {
				t.Fatalf("fields = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateProductWithoutOptions(t *testing.T) {
	t.Parallel()
	out, err := domain.ValidateProduct(domain.ProductInput{Title: "Gift card", Slug: " Gift-Card ", Status: "active",
		Variants: []domain.VariantInput{{PriceMinor: 0}}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Slug != "gift-card" || len(out.Variants[0].OptionValues) != 0 {
		t.Fatalf("got %+v", out)
	}
}

func TestParseProductStatus(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"draft", "active", "archived"} {
		if _, ok := domain.ParseProductStatus(s); !ok {
			t.Errorf("%q rejected", s)
		}
	}
	if _, ok := domain.ParseProductStatus("deleted"); ok {
		t.Error("deleted accepted")
	}
}

func TestStock(t *testing.T) {
	t.Parallel()
	tests := []struct {
		s     domain.Stock
		qty   int
		avail int
		ok    bool
	}{
		{domain.Stock{Tracked: true, OnHand: 10, Reserved: 3}, 7, 7, true},
		{domain.Stock{Tracked: true, OnHand: 10, Reserved: 3}, 8, 7, false},
		{domain.Stock{Tracked: true, OnHand: 0}, 1, 0, false},
		{domain.Stock{Tracked: false}, 1000, 0, true},
	}
	for _, tt := range tests {
		if got := tt.s.Available(); got != tt.avail {
			t.Errorf("%+v Available = %d, want %d", tt.s, got, tt.avail)
		}
		if got := tt.s.CanSell(tt.qty); got != tt.ok {
			t.Errorf("%+v CanSell(%d) = %v, want %v", tt.s, tt.qty, got, tt.ok)
		}
	}
}

func TestValidateReserve(t *testing.T) {
	t.Parallel()
	gen := ulid.NewGenerator()
	a, b := gen.New(), gen.New()
	out, err := domain.ValidateReserve(domain.ReserveRequest{Ref: "checkout:01J9", Lines: []domain.ReserveLine{{b, 1}, {a, 2}}})
	if err != nil {
		t.Fatal(err)
	}
	if out.TTL != domain.DefaultReservationTTL || out.Lines[0].VariantID != a || out.Lines[1].VariantID != b {
		t.Fatalf("not normalized: %+v", out)
	}
	if !(domain.Reservation{Lines: out.Lines}).SameLines(out) {
		t.Fatal("SameLines")
	}
	if (domain.Reservation{Lines: out.Lines[:1]}).SameLines(out) {
		t.Fatal("SameLines on a subset")
	}

	tests := []struct {
		name string
		in   domain.ReserveRequest
		want []string
	}{
		{"empty ref", domain.ReserveRequest{Lines: []domain.ReserveLine{{a, 1}}}, []string{"ref:invalid"}},
		{"bad ref", domain.ReserveRequest{Ref: "a b", Lines: []domain.ReserveLine{{a, 1}}}, []string{"ref:invalid"}},
		{"long ref", domain.ReserveRequest{Ref: strings.Repeat("r", 101), Lines: []domain.ReserveLine{{a, 1}}}, []string{"ref:invalid"}},
		{"short ttl", domain.ReserveRequest{Ref: "r", TTL: time.Second, Lines: []domain.ReserveLine{{a, 1}}}, []string{"ttl:range"}},
		{"long ttl", domain.ReserveRequest{Ref: "r", TTL: 25 * time.Hour, Lines: []domain.ReserveLine{{a, 1}}}, []string{"ttl:range"}},
		{"no lines", domain.ReserveRequest{Ref: "r"}, []string{"lines:count"}},
		{"too many lines", domain.ReserveRequest{Ref: "r", Lines: make([]domain.ReserveLine, domain.MaxReservationLines+1)}, nil},
		{"zero qty", domain.ReserveRequest{Ref: "r", Lines: []domain.ReserveLine{{a, 0}}}, []string{"lines[0].qty:range"}},
		{"huge qty", domain.ReserveRequest{Ref: "r", Lines: []domain.ReserveLine{{a, domain.MaxReserveQty + 1}}}, []string{"lines[0].qty:range"}},
		{"no variant", domain.ReserveRequest{Ref: "r", Lines: []domain.ReserveLine{{Qty: 1}}}, []string{"lines[0].variant_id:required"}},
		{"dup variant", domain.ReserveRequest{Ref: "r", Lines: []domain.ReserveLine{{a, 1}, {a, 1}}}, []string{"lines[1].variant_id:duplicate"}},
	}
	for _, tt := range tests {
		_, err := domain.ValidateReserve(tt.in)
		got := fieldCodes(t, err)
		if tt.want != nil && !slices.Equal(got, tt.want) {
			t.Errorf("%s: fields = %q, want %q", tt.name, got, tt.want)
		}
		if tt.want == nil && got[0] != "lines:count" {
			t.Errorf("%s: fields = %q", tt.name, got)
		}
	}
}

func TestValidateRefAndStock(t *testing.T) {
	t.Parallel()
	if err := domain.ValidateRef("order-1"); err != nil {
		t.Fatal(err)
	}
	if err := domain.ValidateRef("x/y"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("bad ref: %v", err)
	}
	if err := domain.ValidateStock(domain.StockInput{OnHand: 5}); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{-1, domain.MaxOnHand + 1} {
		if err := domain.ValidateStock(domain.StockInput{OnHand: n}); !errs.Is(err, errs.KindValidation) {
			t.Fatalf("on_hand %d: %v", n, err)
		}
	}
}

func TestValidateCategory(t *testing.T) {
	t.Parallel()
	out, err := domain.ValidateCategory(domain.CategoryInput{Name: " ملابس رجالي ", Position: 2})
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != "ملابس رجالي" || out.Slug != "ملابس-رجالي" {
		t.Fatalf("got %+v", out)
	}
	tests := []struct {
		in   domain.CategoryInput
		want []string
	}{
		{domain.CategoryInput{Name: ""}, []string{"name:length", "slug:required"}},
		{domain.CategoryInput{Name: "A", Slug: "-x"}, []string{"slug:invalid"}},
		{domain.CategoryInput{Name: "A", Position: -1}, []string{"position:range"}},
		{domain.CategoryInput{Name: "A", Position: domain.MaxCategoryPos + 1}, []string{"position:range"}},
	}
	for _, tt := range tests {
		_, err := domain.ValidateCategory(tt.in)
		if got := fieldCodes(t, err); !slices.Equal(got, tt.want) {
			t.Errorf("%+v: fields = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCheckPlacement(t *testing.T) {
	t.Parallel()
	gen := ulid.NewGenerator()
	self, p1, p2 := gen.New(), gen.New(), gen.New()
	tests := []struct {
		name   string
		id     ulid.ID
		height int
		chain  []ulid.ID
		want   error
	}{
		{"new root", ulid.ID{}, 1, nil, nil},
		{"new at depth 3", ulid.ID{}, 1, []ulid.ID{p2, p1}, nil},
		{"new at depth 4", ulid.ID{}, 1, []ulid.ID{p2, p1, gen.New()}, domain.ErrCategoryTooDeep},
		{"move subtree of 2 under depth 2", self, 2, []ulid.ID{p2, p1}, domain.ErrCategoryTooDeep},
		{"move subtree of 2 under root", self, 2, []ulid.ID{p1}, nil},
		{"under itself", self, 1, []ulid.ID{self}, domain.ErrCategoryCycle},
		{"under a descendant", self, 3, []ulid.ID{p2, self}, domain.ErrCategoryCycle},
	}
	for _, tt := range tests {
		if got := domain.CheckPlacement(tt.id, tt.height, tt.chain); !errors.Is(got, tt.want) {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}
