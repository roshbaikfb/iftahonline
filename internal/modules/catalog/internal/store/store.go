// Package store adapts the sqlc queries in catalogdb to domain types.
// Every function runs on a transaction opened by postgres.InTenantTx and
// takes the store ID from tenant.FromContext, never from its arguments
// (CLAUDE.md rule 5); RLS checks it a second time.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/store/catalogdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Tx is the tenant transaction the functions run on.
type Tx = catalogdb.DBTX

func current(ctx context.Context) (ulid.ID, error) {
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return ulid.ID{}, fmt.Errorf("catalog store: %w", err)
	}
	return id.ID, nil
}

// Errors for constraint violations, mapped to the field that caused them.
var (
	errSlugTaken = errs.Validation(errs.FieldError{Field: "slug", Code: "taken", Message: "another product already uses this slug"})
	errSKUTaken  = errs.Validation(errs.FieldError{Field: "variants", Code: "sku_taken",
		Message: "a SKU is already used by another product's variant"})
	errUnknownCategory  = errs.Validation(errs.FieldError{Field: "category_ids", Code: "unknown", Message: "a category does not exist"})
	errUnknownSizeChart = errs.Validation(errs.FieldError{Field: "size_chart_id", Code: "unknown", Message: "the size chart does not exist"})
)

func productErr(err error, op string) error {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.ErrNotFound
	case postgres.IsUniqueViolation(err, "products_store_slug_key"):
		return errSlugTaken
	case postgres.IsUniqueViolation(err, "variants_store_sku_key"):
		return errSKUTaken
	case postgres.IsForeignKeyViolation(err, "product_categories_category_fkey"):
		return errUnknownCategory
	case postgres.IsForeignKeyViolation(err, "products_size_chart_fkey"):
		return errUnknownSizeChart
	}
	return fmt.Errorf("catalog store: %s: %w", op, err)
}

// SaveProduct creates (isNew) or updates the product row.
func SaveProduct(ctx context.Context, tx Tx, id ulid.ID, in domain.ProductInput, isNew bool) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	opts, err := json.Marshal(in.Options)
	if err != nil {
		return fmt.Errorf("catalog store: encode options: %w", err)
	}
	q := catalogdb.New(tx)
	if isNew {
		_, err = q.InsertProduct(ctx, catalogdb.InsertProductParams{
			ID: id, StoreID: store, Slug: in.Slug, Title: in.Title, Description: in.Description, Status: in.Status, Options: opts,
			SizeChartID: in.SizeChartID})
	} else {
		_, err = q.UpdateProduct(ctx, catalogdb.UpdateProductParams{
			ID: id, StoreID: store, Slug: in.Slug, Title: in.Title, Description: in.Description, Status: in.Status, Options: opts,
			SizeChartID: in.SizeChartID})
	}
	if err != nil {
		return productErr(err, "save product")
	}
	return nil
}

// LockProduct locks a live product row for the rest of the transaction,
// so concurrent updates of one product apply one after the other.
func LockProduct(ctx context.Context, tx Tx, id ulid.ID) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	_, err = catalogdb.New(tx).LockProduct(ctx, catalogdb.LockProductParams{StoreID: store, ID: id})
	if err != nil {
		return productErr(err, "lock product")
	}
	return nil
}

// SoftDeleteProduct deletes a live product and its variants.
func SoftDeleteProduct(ctx context.Context, tx Tx, id ulid.ID) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	q := catalogdb.New(tx)
	n, err := q.SoftDeleteProduct(ctx, catalogdb.SoftDeleteProductParams{StoreID: store, ID: id})
	if err != nil {
		return productErr(err, "delete product")
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	if err = q.SoftDeleteVariantsExcept(ctx, catalogdb.SoftDeleteVariantsExceptParams{StoreID: store, ProductID: id, Keep: []ulid.ID{}}); err != nil {
		return productErr(err, "delete variants")
	}
	return nil
}

// NewVariant is a variant to insert, with its ID and position decided.
type NewVariant struct {
	ID       ulid.ID
	Position int
	In       domain.VariantInput
}

// variantBatch is the parallel-array form of a set of variants.
type variantBatch struct {
	ids        []ulid.ID
	skus       []string
	options    [][]byte
	prices     []int64
	compareAts []int64
	costs      []int64 // -1: none sent
	positions  []int32
	hidden     []bool
}

func toBatch(vs []NewVariant) (variantBatch, error) {
	b := variantBatch{
		ids: make([]ulid.ID, len(vs)), skus: make([]string, len(vs)), options: make([][]byte, len(vs)),
		prices: make([]int64, len(vs)), compareAts: make([]int64, len(vs)), costs: make([]int64, len(vs)), positions: make([]int32, len(vs)),
		hidden: make([]bool, len(vs)),
	}
	for i, v := range vs {
		opts, err := json.Marshal(v.In.OptionValues)
		if err != nil {
			return variantBatch{}, fmt.Errorf("catalog store: encode option values: %w", err)
		}
		b.ids[i], b.skus[i], b.options[i], b.prices[i], b.hidden[i] = v.ID, v.In.SKU, opts, v.In.PriceMinor, v.In.Hidden
		b.positions[i] = int32(v.Position) //nolint:gosec // bounded by domain.MaxVariants
		if v.In.CompareAtMinor != nil {
			b.compareAts[i] = *v.In.CompareAtMinor // 0 means none: a compare-at price is always > price >= 0
		}
		b.costs[i] = -1 // none sent: none for a new variant, the known one kept on an update
		if v.In.CostMinor != nil {
			b.costs[i] = *v.In.CostMinor
		}
	}
	return b, nil
}

// InsertVariants creates variants and their inventory levels.
func InsertVariants(ctx context.Context, tx Tx, productID ulid.ID, cur money.Currency, vs []NewVariant) error {
	if len(vs) == 0 {
		return nil
	}
	store, err := current(ctx)
	if err != nil {
		return err
	}
	b, err := toBatch(vs)
	if err != nil {
		return err
	}
	q := catalogdb.New(tx)
	err = q.InsertVariants(ctx, catalogdb.InsertVariantsParams{
		Ids: b.ids, StoreID: store, ProductID: productID, Skus: b.skus, OptionValues: b.options,
		Prices: b.prices, CompareAts: b.compareAts, Currency: string(cur), Positions: b.positions, Hidden: b.hidden, Costs: b.costs,
	})
	if err != nil {
		return productErr(err, "insert variants")
	}
	tracked := make([]bool, len(vs))
	onHand := make([]int32, len(vs))
	for i, v := range vs {
		tracked[i] = v.In.IsTracked()
		onHand[i] = int32(v.In.OnHand) //nolint:gosec // bounded by domain.MaxOnHand
	}
	if err = q.InsertInventoryLevels(ctx, catalogdb.InsertInventoryLevelsParams{VariantIds: b.ids, StoreID: store, Tracked: tracked, OnHand: onHand}); err != nil {
		return productErr(err, "insert inventory")
	}
	return nil
}

// UpdateVariants saves existing variants (stock is not touched). It
// returns how many rows matched.
func UpdateVariants(ctx context.Context, tx Tx, productID ulid.ID, vs []NewVariant) (int, error) {
	if len(vs) == 0 {
		return 0, nil
	}
	store, err := current(ctx)
	if err != nil {
		return 0, err
	}
	b, err := toBatch(vs)
	if err != nil {
		return 0, err
	}
	n, err := catalogdb.New(tx).UpdateVariants(ctx, catalogdb.UpdateVariantsParams{
		Ids: b.ids, StoreID: store, ProductID: productID, Skus: b.skus, OptionValues: b.options,
		Prices: b.prices, CompareAts: b.compareAts, Positions: b.positions, Hidden: b.hidden, Costs: b.costs,
	})
	if err != nil {
		return 0, productErr(err, "update variants")
	}
	return int(n), nil
}

// SoftDeleteVariantsExcept deletes the product's live variants not in keep.
func SoftDeleteVariantsExcept(ctx context.Context, tx Tx, productID ulid.ID, keep []ulid.ID) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	if keep == nil {
		keep = []ulid.ID{}
	}
	err = catalogdb.New(tx).SoftDeleteVariantsExcept(ctx, catalogdb.SoftDeleteVariantsExceptParams{StoreID: store, ProductID: productID, Keep: keep})
	if err != nil {
		return productErr(err, "delete variants")
	}
	return nil
}

// ProductVariantIDs returns the IDs of the product's live variants.
func ProductVariantIDs(ctx context.Context, tx Tx, productID ulid.ID) ([]ulid.ID, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	ids, err := catalogdb.New(tx).ListProductVariantIDs(ctx, catalogdb.ListProductVariantIDsParams{StoreID: store, ProductID: productID})
	if err != nil {
		return nil, productErr(err, "list variant ids")
	}
	return ids, nil
}

// SetProductCategories replaces the product's categories.
func SetProductCategories(ctx context.Context, tx Tx, productID ulid.ID, ids []ulid.ID) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	q := catalogdb.New(tx)
	if err = q.DeleteProductCategories(ctx, catalogdb.DeleteProductCategoriesParams{StoreID: store, ProductID: productID}); err != nil {
		return productErr(err, "clear categories")
	}
	if len(ids) == 0 {
		return nil
	}
	err = q.InsertProductCategories(ctx, catalogdb.InsertProductCategoriesParams{StoreID: store, ProductID: productID, CategoryIds: ids})
	if err != nil {
		return productErr(err, "set categories")
	}
	return nil
}

// ProductFilter selects a page of products.
type ProductFilter struct {
	Status     *domain.ProductStatus
	CategoryID *ulid.ID
	After      *ulid.ID // last ID of the previous page
	// Stock keeps in_stock, low (1 to LowThreshold units free) or out
	// products; nil keeps all.
	Stock        *string
	LowThreshold int
	Limit        int
}

// ListProducts returns a page of products, newest first, with their
// variants, categories and images, in exactly three queries whatever the
// page size.
func ListProducts(ctx context.Context, tx Tx, f ProductFilter) ([]domain.Product, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	var status *string
	if f.Status != nil {
		s := string(*f.Status)
		status = &s
	}
	lim := int32(f.Limit)        //nolint:gosec // bounded by domain.MaxProductsPageLimit
	low := int32(f.LowThreshold) //nolint:gosec // bounded by domain.MaxLowThreshold
	q := catalogdb.New(tx)
	var rows []catalogdb.ListProductsRow
	if f.CategoryID != nil {
		var inCat []catalogdb.ListProductsInCategoryRow
		inCat, err = q.ListProductsInCategory(ctx, catalogdb.ListProductsInCategoryParams{
			StoreID: store, CategoryID: *f.CategoryID, Status: status, After: f.After, Stock: f.Stock, LowThreshold: low, Lim: lim,
		})
		rows = make([]catalogdb.ListProductsRow, len(inCat))
		for i, r := range inCat {
			rows[i] = catalogdb.ListProductsRow(r)
		}
	} else {
		rows, err = q.ListProducts(ctx, catalogdb.ListProductsParams{StoreID: store, Status: status, After: f.After, Stock: f.Stock, LowThreshold: low, Lim: lim})
	}
	if err != nil {
		return nil, productErr(err, "list products")
	}
	return loadProducts(ctx, tx, store, rows)
}

// GetProduct returns a live product with its variants and categories.
func GetProduct(ctx context.Context, tx Tx, id ulid.ID) (domain.Product, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Product{}, err
	}
	row, err := catalogdb.New(tx).GetProduct(ctx, catalogdb.GetProductParams{StoreID: store, ID: id})
	if err != nil {
		return domain.Product{}, productErr(err, "get product")
	}
	ps, err := loadProducts(ctx, tx, store, []catalogdb.ListProductsRow{catalogdb.ListProductsRow(row)})
	if err != nil {
		return domain.Product{}, err
	}
	return ps[0], nil
}

// loadProducts attaches variants and images to rows (which carry their
// category IDs) with one query each for the whole page (no N+1).
func loadProducts(ctx context.Context, tx Tx, store ulid.ID, rows []catalogdb.ListProductsRow) ([]domain.Product, error) {
	if len(rows) == 0 {
		return []domain.Product{}, nil
	}
	out := make([]domain.Product, len(rows))
	index := make(map[ulid.ID]int, len(rows))
	ids := make([]ulid.ID, len(rows))
	for i, r := range rows {
		p, err := toProduct(r)
		if err != nil {
			return nil, err
		}
		out[i], index[r.ID], ids[i] = p, i, r.ID
	}
	vs, err := catalogdb.New(tx).ListVariantsByProducts(ctx, catalogdb.ListVariantsByProductsParams{StoreID: store, ProductIds: ids})
	if err != nil {
		return nil, productErr(err, "list variants")
	}
	for _, v := range vs {
		var dv domain.Variant
		if dv, err = toVariant(v); err != nil {
			return nil, err
		}
		p := &out[index[v.ProductID]]
		p.Variants = append(p.Variants, dv)
	}
	if err = loadImages(ctx, tx, store, ids, out, index); err != nil {
		return nil, err
	}
	return out, nil
}

func toProduct(r catalogdb.ListProductsRow) (domain.Product, error) {
	var opts []domain.Option
	if err := json.Unmarshal(r.Options, &opts); err != nil {
		return domain.Product{}, fmt.Errorf("catalog store: decode options of %s: %w", r.ID, err)
	}
	if opts == nil {
		opts = []domain.Option{}
	}
	return domain.Product{
		ID: r.ID, Slug: r.Slug, Title: r.Title, Description: r.Description, Status: domain.ProductStatus(r.Status),
		Options: opts, Variants: []domain.Variant{}, Images: []domain.Image{}, CategoryIDs: r.CategoryIds, Sample: r.IsSample, SizeChartID: r.SizeChartID, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}, nil
}

func toVariant(v catalogdb.ListVariantsByProductsRow) (domain.Variant, error) {
	price, compareAt, err := prices(v.Currency, v.PriceMinor, v.CompareAtMinor)
	if err != nil {
		return domain.Variant{}, err
	}
	cost, err := costOf(v.Currency, v.CostMinor)
	if err != nil {
		return domain.Variant{}, err
	}
	return domain.Variant{
		ID: v.ID, ProductID: v.ProductID, SKU: deref(v.Sku), OptionValues: v.OptionValues,
		Price: price, CompareAt: compareAt, Cost: cost, Position: int(v.Position), Hidden: v.Hidden,
		Stock: domain.Stock{Tracked: v.Tracked, OnHand: int(v.OnHand), Reserved: int(v.Reserved)},
	}, nil
}

func prices(cur string, price int64, compareAt *int64) (money.Money, *money.Money, error) {
	p, err := money.New(price, money.Currency(cur))
	if err != nil {
		return money.Money{}, nil, fmt.Errorf("catalog store: price: %w", err)
	}
	if compareAt == nil {
		return p, nil, nil
	}
	c, err := money.New(*compareAt, money.Currency(cur))
	if err != nil {
		return money.Money{}, nil, fmt.Errorf("catalog store: compare-at price: %w", err)
	}
	return p, &c, nil
}

// costOf is a variant's cost, nil when not known.
func costOf(cur string, minor *int64) (*money.Money, error) {
	if minor == nil {
		return nil, nil //nolint:nilnil // no cost is not an error
	}
	c, err := money.New(*minor, money.Currency(cur))
	if err != nil {
		return nil, fmt.Errorf("catalog store: cost: %w", err)
	}
	return &c, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// MarkSample marks a product the setup wizard's template added.
func MarkSample(ctx context.Context, tx Tx, id ulid.ID) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	n, err := catalogdb.New(tx).MarkSample(ctx, catalogdb.MarkSampleParams{StoreID: store, ID: id})
	if err != nil {
		return fmt.Errorf("catalog store: mark sample: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// SampleIDs lists up to 200 of the store's sample products.
func SampleIDs(ctx context.Context, tx Tx) ([]ulid.ID, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	ids, err := catalogdb.New(tx).ListSampleIDs(ctx, store)
	if err != nil {
		return nil, fmt.Errorf("catalog store: sample ids: %w", err)
	}
	return ids, nil
}

// LowStock lists up to limit variants with at most threshold units free,
// the fewest first, and how many there are in all.
func LowStock(ctx context.Context, tx Tx, threshold, limit int) ([]domain.LowStockItem, int, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := catalogdb.New(tx).LowStock(ctx, catalogdb.LowStockParams{StoreID: store, Threshold: int32(threshold), Lim: int32(limit)}) //nolint:gosec // bounded by the caller
	if err != nil {
		return nil, 0, fmt.Errorf("catalog store: low stock: %w", err)
	}
	out := make([]domain.LowStockItem, len(rows))
	total := 0
	for i, r := range rows {
		out[i] = domain.LowStockItem{VariantID: r.VariantID, ProductID: r.ProductID, ProductTitle: r.ProductTitle,
			OptionValues: r.OptionValues, Available: int(r.Available)}
		total = int(r.Total)
	}
	return out, total, nil
}

// CountProducts counts the store's live products by status.
func CountProducts(ctx context.Context, tx Tx) (map[domain.ProductStatus]int, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := catalogdb.New(tx).CountProductsByStatus(ctx, store)
	if err != nil {
		return nil, productErr(err, "count products")
	}
	out := make(map[domain.ProductStatus]int, len(rows))
	for _, r := range rows {
		out[domain.ProductStatus(r.Status)] = int(r.N)
	}
	return out, nil
}

// CountLiveProducts is how many products the store has (any status, not
// deleted): what the plan's products limit counts. Its signature fits
// entitlements' AdmitTx.
func CountLiveProducts(ctx context.Context, tx pgx.Tx) (int64, error) {
	store, err := current(ctx)
	if err != nil {
		return 0, err
	}
	n, err := catalogdb.New(tx).CountLiveProducts(ctx, store)
	if err != nil {
		return 0, productErr(err, "count live products")
	}
	return n, nil
}

// SetProductsStatus sets the status of the store's live products among
// ids and returns the IDs it changed.
func SetProductsStatus(ctx context.Context, tx Tx, ids []ulid.ID, status domain.ProductStatus) ([]ulid.ID, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	out, err := catalogdb.New(tx).SetProductsStatus(ctx, catalogdb.SetProductsStatusParams{StoreID: store, Ids: ids, Status: string(status)})
	if err != nil {
		return nil, productErr(err, "set products status")
	}
	return out, nil
}
