// Package app holds the catalog use cases. Every method acts on the store
// in ctx (tenant.FromContext) inside one tenant transaction, and records
// merchant actions in the audit log in that same transaction.
package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// CurrencySource returns the currency of the store in ctx. The tenancy
// module provides it; the catalog never reads the stores table.
type CurrencySource interface {
	StoreCurrency(ctx context.Context) (money.Currency, error)
}

// Service implements the catalog use cases.
type Service struct {
	db       postgres.Beginner
	jobs     jobs.Inserter
	ids      *ulid.Generator
	audit    audit.Recorder
	currency CurrencySource
	media    Media
	search   SearchIndex
	imports  Imports
	now      func() time.Time
}

// Deps are the service's dependencies.
type Deps struct {
	DB       postgres.Beginner
	Jobs     jobs.Inserter
	IDs      *ulid.Generator
	Audit    audit.Recorder
	Currency CurrencySource
	Media    Media
	Search   SearchIndex
	Imports  Imports
}

// NewService builds the service.
func NewService(d Deps) *Service {
	return &Service{db: d.DB, jobs: d.Jobs, ids: d.IDs, audit: d.Audit, currency: d.Currency, media: d.Media, search: d.Search, imports: d.Imports, now: time.Now}
}

// SetClock replaces the clock (tests).
func (s *Service) SetClock(now func() time.Time) { s.now = now }

var (
	errProductNotFound = errs.NotFound("product_not_found", "product not found")
	errBadCursor       = errs.Validation(errs.FieldError{Field: "cursor", Code: "invalid", Message: "invalid cursor"})
)

func notFound(err error, what error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return what
	}
	return err
}

// CreateProduct creates a product with its variants and categories.
func (s *Service) CreateProduct(ctx context.Context, in domain.ProductInput) (domain.Product, error) {
	in, err := domain.ValidateProduct(in)
	if err != nil {
		return domain.Product{}, err
	}
	var fields []errs.FieldError
	for i, v := range in.Variants {
		if v.ID != nil {
			fields = append(fields, errs.FieldError{Field: fmt.Sprintf("variants[%d].id", i), Code: "not_allowed", Message: "new products cannot reference existing variants"})
		}
	}
	if len(fields) > 0 {
		return domain.Product{}, errs.Validation(fields...)
	}
	cur, err := s.currency.StoreCurrency(ctx)
	if err != nil {
		return domain.Product{}, fmt.Errorf("catalog: store currency: %w", err)
	}
	id := s.ids.New()
	var p domain.Product
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		p, txErr = s.createTx(ctx, tx, id, cur, in)
		return txErr
	})
	return p, err
}

func (s *Service) createTx(ctx context.Context, tx pgx.Tx, id ulid.ID, cur money.Currency, in domain.ProductInput) (domain.Product, error) {
	if err := store.SaveProduct(ctx, tx, id, in, true); err != nil {
		return domain.Product{}, err
	}
	if err := store.InsertVariants(ctx, tx, id, cur, s.newVariants(in.Variants, nil)); err != nil {
		return domain.Product{}, err
	}
	if err := store.SetProductCategories(ctx, tx, id, in.CategoryIDs); err != nil {
		return domain.Product{}, err
	}
	if err := s.auditProduct(ctx, tx, "product.created", id, in.Title); err != nil {
		return domain.Product{}, err
	}
	if err := s.reindex(ctx, tx, id); err != nil {
		return domain.Product{}, err
	}
	return store.GetProduct(ctx, tx, id)
}

// newVariants assigns IDs and positions to the variants to insert: those
// without an ID, or every variant when existing is nil.
func (s *Service) newVariants(in []domain.VariantInput, existing map[ulid.ID]bool) []store.NewVariant {
	out := make([]store.NewVariant, 0, len(in))
	for i, v := range in {
		if v.ID != nil && existing != nil {
			continue
		}
		out = append(out, store.NewVariant{ID: s.ids.New(), Position: i, In: v})
	}
	return out
}

// UpdateProduct replaces a product: its fields, options, variants and
// categories. Variants keep their identity and stock by ID; new ones are
// created and missing ones are deleted.
func (s *Service) UpdateProduct(ctx context.Context, id ulid.ID, in domain.ProductInput) (domain.Product, error) {
	in, err := domain.ValidateProduct(in)
	if err != nil {
		return domain.Product{}, err
	}
	cur, err := s.currency.StoreCurrency(ctx)
	if err != nil {
		return domain.Product{}, fmt.Errorf("catalog: store currency: %w", err)
	}
	var p domain.Product
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		p, txErr = s.replaceTx(ctx, tx, id, cur, in)
		return txErr
	})
	return p, notFound(err, errProductNotFound)
}

func (s *Service) replaceTx(ctx context.Context, tx pgx.Tx, id ulid.ID, cur money.Currency, in domain.ProductInput) (domain.Product, error) {
	if err := store.LockProduct(ctx, tx, id); err != nil {
		return domain.Product{}, err
	}
	ids, err := store.ProductVariantIDs(ctx, tx, id)
	if err != nil {
		return domain.Product{}, err
	}
	existing := make(map[ulid.ID]bool, len(ids))
	for _, vid := range ids {
		existing[vid] = true
	}
	var keep []ulid.ID
	var updates []store.NewVariant
	var fields []errs.FieldError
	for i, v := range in.Variants {
		if v.ID == nil {
			continue
		}
		if !existing[*v.ID] {
			fields = append(fields, errs.FieldError{Field: fmt.Sprintf("variants[%d].id", i), Code: "unknown", Message: "this variant is not part of the product"})
			continue
		}
		keep = append(keep, *v.ID)
		updates = append(updates, store.NewVariant{ID: *v.ID, Position: i, In: v})
	}
	if len(fields) > 0 {
		return domain.Product{}, errs.Validation(fields...)
	}
	// Delete first, so a removed variant's SKU and option values are free
	// for the variants that follow.
	if err = store.SoftDeleteVariantsExcept(ctx, tx, id, keep); err != nil {
		return domain.Product{}, err
	}
	if _, err = store.UpdateVariants(ctx, tx, id, updates); err != nil {
		return domain.Product{}, err
	}
	if err = store.InsertVariants(ctx, tx, id, cur, s.newVariants(in.Variants, existing)); err != nil {
		return domain.Product{}, err
	}
	if err = store.SaveProduct(ctx, tx, id, in, false); err != nil {
		return domain.Product{}, err
	}
	if err = store.SetProductCategories(ctx, tx, id, in.CategoryIDs); err != nil {
		return domain.Product{}, err
	}
	if err = s.auditProduct(ctx, tx, "product.updated", id, in.Title); err != nil {
		return domain.Product{}, err
	}
	if err = s.reindex(ctx, tx, id); err != nil {
		return domain.Product{}, err
	}
	return store.GetProduct(ctx, tx, id)
}

// GetProduct returns a live product.
func (s *Service) GetProduct(ctx context.Context, id ulid.ID) (domain.Product, error) {
	var p domain.Product
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		p, err = store.GetProduct(ctx, tx, id)
		return err
	})
	return p, notFound(err, errProductNotFound)
}

// DeleteProduct soft-deletes a product and its variants. Its slug and SKUs
// become free; existing reservations are unaffected. Its images are
// deleted, and a job removes their objects.
func (s *Service) DeleteProduct(ctx context.Context, id ulid.ID) error {
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if err := store.SoftDeleteProduct(ctx, tx, id); err != nil {
			return err
		}
		keys, err := store.DeleteProductImages(ctx, tx, id)
		if err != nil {
			return err
		}
		if err = s.deleteObjects(ctx, tx, keys); err != nil {
			return err
		}
		if err = s.reindex(ctx, tx, id); err != nil {
			return err
		}
		return s.auditProduct(ctx, tx, "product.deleted", id, "")
	})
	return notFound(err, errProductNotFound)
}

// ProductQuery selects a page of products.
type ProductQuery struct {
	Status     string
	CategoryID *ulid.ID
	Cursor     string
	Limit      int
}

// ProductPage is one page of products, newest first.
type ProductPage struct {
	Items      []domain.Product
	NextCursor string // empty on the last page
}

// ListProducts returns a page of products with their variants and
// categories in a constant number of queries.
func (s *Service) ListProducts(ctx context.Context, q ProductQuery) (ProductPage, error) {
	f := store.ProductFilter{CategoryID: q.CategoryID, Limit: q.Limit}
	if f.Limit <= 0 {
		f.Limit = domain.DefaultProductsLimit
	}
	f.Limit = min(f.Limit, domain.MaxProductsPageLimit)
	if q.Status != "" {
		st, ok := domain.ParseProductStatus(q.Status)
		if !ok {
			return ProductPage{}, errs.Validation(errs.FieldError{Field: "status", Code: "invalid", Message: "status must be draft, active or archived"})
		}
		f.Status = &st
	}
	if q.Cursor != "" {
		after, err := ulid.Parse(q.Cursor)
		if err != nil {
			return ProductPage{}, errBadCursor
		}
		f.After = &after
	}
	// One extra row tells whether another page follows.
	want := f.Limit
	f.Limit++
	var items []domain.Product
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		items, err = store.ListProducts(ctx, tx, f)
		return err
	})
	if err != nil {
		return ProductPage{}, err
	}
	page := ProductPage{Items: items}
	if len(items) > want {
		page.Items = items[:want]
		page.NextCursor = page.Items[want-1].ID.String()
	}
	return page, nil
}

// SetStock sets a variant's stock level.
func (s *Service) SetStock(ctx context.Context, variantID ulid.ID, in domain.StockInput) (domain.Stock, error) {
	if err := domain.ValidateStock(in); err != nil {
		return domain.Stock{}, err
	}
	var st domain.Stock
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		if st, err = store.SetStock(ctx, tx, variantID, in); err != nil {
			return err
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "inventory.updated", TargetType: "variant", TargetID: variantID.String(),
			Metadata: map[string]any{"on_hand": in.OnHand, "tracked": in.Tracked}})
	})
	return st, notFound(err, ErrVariantNotFound)
}

func auditEntry(action, target string, id ulid.ID) audit.Entry {
	return audit.Entry{Action: action, TargetType: target, TargetID: id.String()}
}

func (s *Service) auditProduct(ctx context.Context, tx pgx.Tx, action string, id ulid.ID, title string) error {
	e := auditEntry(action, "product", id)
	if title != "" {
		e.Metadata = map[string]any{"title": title}
	}
	return s.audit.RecordTx(ctx, tx, e)
}
