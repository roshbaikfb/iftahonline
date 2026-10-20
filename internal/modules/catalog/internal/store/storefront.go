package store

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/store/catalogdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// GetActiveProductBySlug returns an active, live product with its
// variants, categories and images.
func GetActiveProductBySlug(ctx context.Context, tx Tx, slug string) (domain.Product, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Product{}, err
	}
	row, err := catalogdb.New(tx).GetActiveProductBySlug(ctx, catalogdb.GetActiveProductBySlugParams{StoreID: store, Slug: slug})
	if err != nil {
		return domain.Product{}, productErr(err, "get product by slug")
	}
	ps, err := loadProducts(ctx, tx, store, []catalogdb.ListProductsRow{catalogdb.ListProductsRow(row)})
	if err != nil {
		return domain.Product{}, err
	}
	return ps[0], nil
}

// ListActiveProductsByIDs returns the active, live products among ids, in
// the order given, in three queries whatever their number.
func ListActiveProductsByIDs(ctx context.Context, tx Tx, ids []ulid.ID) ([]domain.Product, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := catalogdb.New(tx).ListActiveProductsByIDs(ctx, catalogdb.ListActiveProductsByIDsParams{StoreID: store, Ids: ids})
	if err != nil {
		return nil, productErr(err, "list products by ids")
	}
	list := make([]catalogdb.ListProductsRow, len(rows))
	for i, r := range rows {
		list[i] = catalogdb.ListProductsRow(r)
	}
	return loadProducts(ctx, tx, store, list)
}
