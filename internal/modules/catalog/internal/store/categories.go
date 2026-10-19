package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/store/catalogdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Category errors.
var (
	errCategorySlugTaken = errs.Validation(errs.FieldError{Field: "slug", Code: "taken", Message: "another category already uses this slug"})
	// ErrUnknownParent means the parent category does not exist.
	ErrUnknownParent = errs.Validation(errs.FieldError{Field: "parent_id", Code: "unknown", Message: "the parent category does not exist"})
	// ErrCategoryHasChildren means a category with children was deleted.
	ErrCategoryHasChildren = errs.Conflict("category_has_children", "move or delete the subcategories first")
)

func categoryErr(err error, op string) error {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return domain.ErrNotFound
	case postgres.IsUniqueViolation(err, "categories_store_slug_key"):
		return errCategorySlugTaken
	case postgres.IsForeignKeyViolation(err, "categories_parent_fkey"):
		return ErrUnknownParent
	}
	return fmt.Errorf("catalog store: %s: %w", op, err)
}

// SaveCategory creates (isNew) or replaces a category.
func SaveCategory(ctx context.Context, tx Tx, id ulid.ID, in domain.CategoryInput, isNew bool) (domain.Category, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Category{}, err
	}
	pos := int32(in.Position) //nolint:gosec // bounded by domain.MaxCategoryPos
	q := catalogdb.New(tx)
	var row catalogdb.Category
	if isNew {
		row, err = q.InsertCategory(ctx, catalogdb.InsertCategoryParams{ID: id, StoreID: store, ParentID: in.ParentID, Slug: in.Slug, Name: in.Name, Position: pos})
	} else {
		row, err = q.UpdateCategory(ctx, catalogdb.UpdateCategoryParams{ID: id, StoreID: store, ParentID: in.ParentID, Slug: in.Slug, Name: in.Name, Position: pos})
	}
	if err != nil {
		return domain.Category{}, categoryErr(err, "save category")
	}
	return toCategory(row), nil
}

// GetCategory returns one category.
func GetCategory(ctx context.Context, tx Tx, id ulid.ID) (domain.Category, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Category{}, err
	}
	row, err := catalogdb.New(tx).GetCategory(ctx, catalogdb.GetCategoryParams{StoreID: store, ID: id})
	if err != nil {
		return domain.Category{}, categoryErr(err, "get category")
	}
	return toCategory(row), nil
}

// DeleteCategory deletes a category without children; products leave it.
func DeleteCategory(ctx context.Context, tx Tx, id ulid.ID) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	n, err := catalogdb.New(tx).DeleteCategory(ctx, catalogdb.DeleteCategoryParams{StoreID: store, ID: id})
	switch {
	case postgres.IsForeignKeyViolation(err, "categories_parent_fkey"):
		return ErrCategoryHasChildren
	case err != nil:
		return categoryErr(err, "delete category")
	case n == 0:
		return domain.ErrNotFound
	}
	return nil
}

// ListCategories returns every category of the store (at most
// domain.MaxCategoriesPerStore), roots first.
func ListCategories(ctx context.Context, tx Tx) ([]domain.Category, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := catalogdb.New(tx).ListCategories(ctx, catalogdb.ListCategoriesParams{StoreID: store, Lim: domain.MaxCategoriesPerStore})
	if err != nil {
		return nil, categoryErr(err, "list categories")
	}
	out := make([]domain.Category, len(rows))
	for i, r := range rows {
		out[i] = toCategory(r)
	}
	return out, nil
}

// CountCategories counts the store's categories.
func CountCategories(ctx context.Context, tx Tx) (int, error) {
	store, err := current(ctx)
	if err != nil {
		return 0, err
	}
	n, err := catalogdb.New(tx).CountCategories(ctx, store)
	if err != nil {
		return 0, categoryErr(err, "count categories")
	}
	return int(n), nil
}

// CategoryChain returns the category and its ancestors, nearest first, or
// ErrNotFound when the category does not exist.
func CategoryChain(ctx context.Context, tx Tx, id ulid.ID) ([]ulid.ID, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	ids, err := catalogdb.New(tx).CategoryAncestors(ctx, catalogdb.CategoryAncestorsParams{StoreID: store, ID: id})
	if err != nil {
		return nil, categoryErr(err, "category ancestors")
	}
	if len(ids) == 0 {
		return nil, domain.ErrNotFound
	}
	return ids, nil
}

// SubtreeHeight returns the number of levels under and including id.
func SubtreeHeight(ctx context.Context, tx Tx, id ulid.ID) (int, error) {
	store, err := current(ctx)
	if err != nil {
		return 0, err
	}
	h, err := catalogdb.New(tx).CategorySubtreeHeight(ctx, catalogdb.CategorySubtreeHeightParams{StoreID: store, ID: id})
	if err != nil {
		return 0, categoryErr(err, "subtree height")
	}
	return int(h), nil
}

func toCategory(r catalogdb.Category) domain.Category {
	return domain.Category{ID: r.ID, ParentID: r.ParentID, Slug: r.Slug, Name: r.Name, Position: int(r.Position),
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}
