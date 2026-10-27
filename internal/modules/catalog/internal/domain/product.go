// Package domain holds the catalog rules: what a product, a variant and a
// category are, which inputs are acceptable, and how stock is reserved.
// It is pure: standard library and internal/platform only (CLAUDE.md
// rule 3).
package domain

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/slug"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// ErrNotFound is returned by the store layer when a row is missing, which
// includes rows of other tenants (RLS hides them) and soft-deleted rows.
var ErrNotFound = errors.New("catalog: not found")

// Limits (ADR-0011). They bound request size, query cost and the size of
// a product page.
const (
	MaxOptions            = 3
	MaxOptionValues       = 50
	MaxVariants           = 100
	MaxProductCategories  = 20
	MaxTitleLen           = 200
	MaxDescriptionLen     = 20000
	MaxOptionNameLen      = 50
	MaxOptionValueLen     = 50
	MaxSKULen             = 64
	MaxPriceMinor         = 1_000_000_000_000 // 10 billion in a 2-digit currency
	MaxOnHand             = 1_000_000
	DefaultProductsLimit  = 25
	MaxProductsPageLimit  = 100
	MaxCategoriesPerStore = 500
	MaxBulkProducts       = 100
	DefaultLowThreshold   = 5
	MaxLowThreshold       = 100
)

// Stock filters of the dashboard's product list.
const (
	StockInStock = "in_stock"
	StockLow     = "low"
	StockOut     = "out"
)

// BulkDelete is the bulk action that deletes products; the others are
// statuses.
const BulkDelete = "delete"

// ProductStatus is a product's lifecycle state. Only active products are
// sellable.
type ProductStatus string

// Product statuses.
const (
	StatusDraft    ProductStatus = "draft"
	StatusActive   ProductStatus = "active"
	StatusArchived ProductStatus = "archived"
)

// ParseProductStatus validates a status.
func ParseProductStatus(s string) (ProductStatus, bool) {
	st := ProductStatus(s)
	return st, slices.Contains([]ProductStatus{StatusDraft, StatusActive, StatusArchived}, st)
}

// Option is a product dimension such as size or colour, with the values a
// variant may take. Colors marks a colour option (ADR-0035): one
// "#rrggbb" per value, or "" where the storefront guesses from the name.
type Option struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
	Colors []string `json:"colors,omitempty"`
}

// Stock is a variant's inventory level.
type Stock struct {
	Tracked  bool
	OnHand   int
	Reserved int
}

// Available is how many units can still be reserved. It is meaningless
// for untracked stock, which never runs out.
func (s Stock) Available() int { return max(s.OnHand-s.Reserved, 0) }

// CanSell reports whether qty more units can be reserved.
func (s Stock) CanSell(qty int) bool { return !s.Tracked || s.Available() >= qty }

// MaxShownLeft is the most units left a shopper may be told about
// ("only 3 left"); above it, stock levels stay private.
const MaxShownLeft = 10

// Left is how many units are left, for a shop's "only N left": tracked
// stock with 1 to MaxShownLeft units; 0 otherwise (plenty, untracked or
// sold out).
func (s Stock) Left() int {
	if n := s.Available(); s.Tracked && n <= MaxShownLeft {
		return n
	}
	return 0
}

// ShownStockChanged reports whether tracked stock going from before to
// after available units changes what a shop shows: sold out, back in
// stock, or the "only N left" count. Changes among plenty show nothing,
// so they need no event (and purge no cached page).
func ShownStockChanged(before, after int) bool {
	return before != after && min(before, after) <= MaxShownLeft
}

// Variant is one sellable combination of a product's option values.
type Variant struct {
	ID           ulid.ID
	ProductID    ulid.ID
	SKU          string
	OptionValues []string
	Price        money.Money
	CompareAt    *money.Money
	Position     int
	Stock        Stock
}

// Product is a catalog item with its variants and category IDs.
type Product struct {
	ID          ulid.ID
	Slug        string
	Title       string
	Description string
	Status      ProductStatus
	Options     []Option
	Variants    []Variant
	CategoryIDs []ulid.ID
	Images      []Image
	// Sample marks a product the setup wizard's template added (ADR-0034):
	// shown, never sellable, deleted with the store's other samples.
	Sample bool
	// SizeChartID is the store's size chart the product page shows; nil
	// for none.
	SizeChartID *ulid.ID
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ProductInput creates or replaces a product. On update, variants with an
// ID keep their identity (and stock); variants without one are new, and
// live variants missing from the input are deleted.
type ProductInput struct {
	Slug        string
	Title       string
	Description string
	Status      string
	Options     []Option
	Variants    []VariantInput
	CategoryIDs []ulid.ID
	SizeChartID *ulid.ID
}

// VariantInput is one variant in a ProductInput.
type VariantInput struct {
	ID             *ulid.ID
	SKU            string
	OptionValues   []string
	PriceMinor     int64
	CompareAtMinor *int64
	// Tracked and OnHand set the initial stock of a new variant; existing
	// variants change stock only through the inventory endpoint.
	Tracked *bool
	OnHand  int
}

// ValidateProduct normalizes in and checks every rule, reporting all
// invalid fields at once. Field names follow the JSON body
// (variants[2].price_minor).
func ValidateProduct(in ProductInput) (ProductInput, error) {
	out := ProductInput{
		Title:       strings.TrimSpace(in.Title),
		Description: strings.TrimSpace(in.Description),
		Status:      strings.TrimSpace(in.Status),
		CategoryIDs: in.CategoryIDs,
		SizeChartID: in.SizeChartID,
	}
	out.Slug = slug.Normalize(in.Slug, out.Title)
	if out.Status == "" {
		out.Status = string(StatusDraft)
	}
	var fields []errs.FieldError
	add := func(field, code, msg string) {
		fields = append(fields, errs.FieldError{Field: field, Code: code, Message: msg})
	}

	if fe, ok := checkText("title", out.Title, MaxTitleLen, false); !ok {
		fields = append(fields, fe)
	}
	if fe, ok := checkSlugOrDerived(out.Slug); !ok {
		fields = append(fields, fe)
	}
	if utf8.RuneCountInString(out.Description) > MaxDescriptionLen || !utf8.ValidString(out.Description) {
		add("description", "length", fmt.Sprintf("description must be at most %d characters", MaxDescriptionLen))
	}
	if _, ok := ParseProductStatus(out.Status); !ok {
		add("status", "invalid", "status must be draft, active or archived")
	}
	if len(out.CategoryIDs) > MaxProductCategories {
		add("category_ids", "too_many", fmt.Sprintf("a product can be in at most %d categories", MaxProductCategories))
	} else if hasDuplicateIDs(out.CategoryIDs) {
		add("category_ids", "duplicate", "category IDs must be unique")
	}

	var optFields []errs.FieldError
	out.Options, optFields = normalizeOptions(in.Options)
	fields = append(fields, optFields...)
	var varFields []errs.FieldError
	out.Variants, varFields = normalizeVariants(in.Variants, out.Options, len(optFields) == 0)
	fields = append(fields, varFields...)

	if len(fields) > 0 {
		return ProductInput{}, errs.Validation(fields...)
	}
	return out, nil
}

func checkSlugOrDerived(s string) (errs.FieldError, bool) {
	if s == "" {
		return errs.FieldError{Field: "slug", Code: "required", Message: "slug is required when the title has no letters or digits"}, false
	}
	return slug.Check("slug", s)
}

// checkText validates a single-line display string.
func checkText(field, s string, maxLen int, allowEmpty bool) (errs.FieldError, bool) {
	n := utf8.RuneCountInString(s)
	if (n == 0 && !allowEmpty) || n > maxLen || !utf8.ValidString(s) {
		return errs.FieldError{Field: field, Code: "length", Message: fmt.Sprintf("%s must be 1 to %d characters", field, maxLen)}, false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return errs.FieldError{Field: field, Code: "invalid", Message: field + " must not contain control characters"}, false
		}
	}
	return errs.FieldError{}, true
}

func normalizeOptions(in []Option) ([]Option, []errs.FieldError) {
	var fields []errs.FieldError
	if len(in) > MaxOptions {
		return nil, []errs.FieldError{{Field: "options", Code: "too_many", Message: fmt.Sprintf("a product can have at most %d options", MaxOptions)}}
	}
	out := make([]Option, len(in))
	names := make(map[string]struct{}, len(in))
	for i, o := range in {
		prefix := fmt.Sprintf("options[%d]", i)
		name := strings.TrimSpace(o.Name)
		if fe, ok := checkText(prefix+".name", name, MaxOptionNameLen, false); !ok {
			fields = append(fields, fe)
		}
		key := strings.ToLower(name)
		if _, dup := names[key]; dup {
			fields = append(fields, errs.FieldError{Field: prefix + ".name", Code: "duplicate", Message: "option names must be unique"})
		}
		names[key] = struct{}{}
		if len(o.Values) == 0 || len(o.Values) > MaxOptionValues {
			fields = append(fields, errs.FieldError{Field: prefix + ".values", Code: "count",
				Message: fmt.Sprintf("an option needs 1 to %d values", MaxOptionValues)})
		}
		values := make([]string, len(o.Values))
		seen := make(map[string]struct{}, len(o.Values))
		for j, v := range o.Values {
			v = strings.TrimSpace(v)
			field := fmt.Sprintf("%s.values[%d]", prefix, j)
			if fe, ok := checkText(field, v, MaxOptionValueLen, false); !ok {
				fields = append(fields, fe)
			}
			if _, dup := seen[strings.ToLower(v)]; dup {
				fields = append(fields, errs.FieldError{Field: field, Code: "duplicate", Message: "option values must be unique"})
			}
			seen[strings.ToLower(v)] = struct{}{}
			values[j] = v
		}
		colors, colorFields := normalizeColors(prefix, o.Colors, len(o.Values))
		fields = append(fields, colorFields...)
		out[i] = Option{Name: name, Values: values, Colors: colors}
	}
	return out, fields
}

// normalizeVariants checks the variants against the (already valid, when
// optionsOK) options.
func normalizeVariants(in []VariantInput, opts []Option, optionsOK bool) ([]VariantInput, []errs.FieldError) {
	switch {
	case len(in) == 0:
		return nil, []errs.FieldError{{Field: "variants", Code: "required", Message: "a product needs at least one variant"}}
	case len(in) > MaxVariants:
		return nil, []errs.FieldError{{Field: "variants", Code: "too_many", Message: fmt.Sprintf("a product can have at most %d variants", MaxVariants)}}
	case optionsOK && len(opts) == 0 && len(in) > 1:
		return nil, []errs.FieldError{{Field: "variants", Code: "too_many", Message: "a product without options has exactly one variant"}}
	}
	var fields []errs.FieldError
	out := make([]VariantInput, len(in))
	combos := make(map[string]struct{}, len(in))
	skus := make(map[string]struct{}, len(in))
	ids := make(map[ulid.ID]struct{}, len(in))
	for i, v := range in {
		prefix := fmt.Sprintf("variants[%d]", i)
		add := func(field, code, msg string) {
			fields = append(fields, errs.FieldError{Field: prefix + "." + field, Code: code, Message: msg})
		}
		nv := v
		nv.SKU = strings.TrimSpace(v.SKU)
		nv.OptionValues = make([]string, len(v.OptionValues))
		for j, ov := range v.OptionValues {
			nv.OptionValues[j] = strings.TrimSpace(ov)
		}
		if v.ID != nil {
			if _, dup := ids[*v.ID]; dup {
				add("id", "duplicate", "variant IDs must be unique")
			}
			ids[*v.ID] = struct{}{}
		}
		if nv.SKU != "" {
			if fe, ok := checkText(prefix+".sku", nv.SKU, MaxSKULen, false); !ok {
				fields = append(fields, fe)
			}
			if _, dup := skus[strings.ToLower(nv.SKU)]; dup {
				add("sku", "duplicate", "SKUs must be unique")
			}
			skus[strings.ToLower(nv.SKU)] = struct{}{}
		}
		if optionsOK {
			if fe, ok := matchOptions(prefix, nv.OptionValues, opts); !ok {
				fields = append(fields, fe)
			} else {
				key := strings.ToLower(strings.Join(nv.OptionValues, "\x00"))
				if _, dup := combos[key]; dup {
					add("option_values", "duplicate", "two variants have the same option values")
				}
				combos[key] = struct{}{}
				nv.OptionValues = canonicalValues(nv.OptionValues, opts)
			}
		}
		if v.PriceMinor < 0 || v.PriceMinor > MaxPriceMinor {
			add("price_minor", "range", "price must be between 0 and 1000000000000 minor units")
		}
		if v.CompareAtMinor != nil && (*v.CompareAtMinor <= v.PriceMinor || *v.CompareAtMinor > MaxPriceMinor) {
			add("compare_at_minor", "range", "compare-at price must be greater than the price")
		}
		if v.OnHand < 0 || v.OnHand > MaxOnHand {
			add("on_hand", "range", fmt.Sprintf("on_hand must be between 0 and %d", MaxOnHand))
		}
		out[i] = nv
	}
	return out, fields
}

// matchOptions checks that values has one valid value per option, in
// option order (compared case-insensitively).
func matchOptions(prefix string, values []string, opts []Option) (errs.FieldError, bool) {
	if len(values) != len(opts) {
		return errs.FieldError{Field: prefix + ".option_values", Code: "count",
			Message: fmt.Sprintf("a variant needs one value for each of the %d options", len(opts))}, false
	}
	for i, v := range values {
		if !slices.ContainsFunc(opts[i].Values, func(o string) bool { return strings.EqualFold(o, v) }) {
			return errs.FieldError{Field: fmt.Sprintf("%s.option_values[%d]", prefix, i), Code: "unknown",
				Message: fmt.Sprintf("%q is not a value of option %q", v, opts[i].Name)}, false
		}
	}
	return errs.FieldError{}, true
}

// canonicalValues spells each value exactly as its option does.
func canonicalValues(values []string, opts []Option) []string {
	out := make([]string, len(values))
	for i, v := range values {
		j := slices.IndexFunc(opts[i].Values, func(o string) bool { return strings.EqualFold(o, v) })
		out[i] = opts[i].Values[j]
	}
	return out
}

func hasDuplicateIDs(ids []ulid.ID) bool {
	seen := make(map[ulid.ID]struct{}, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			return true
		}
		seen[id] = struct{}{}
	}
	return false
}

// IsTracked returns whether a new variant tracks stock (the default).
func (v VariantInput) IsTracked() bool { return v.Tracked == nil || *v.Tracked }
