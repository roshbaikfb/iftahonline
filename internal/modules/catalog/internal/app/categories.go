package app

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var (
	errCategoryNotFound = errs.NotFound("category_not_found", "category not found")
	errCategoryLimit    = errs.Conflict("category_limit", fmt.Sprintf("a store can have at most %d categories", domain.MaxCategoriesPerStore))
)

// ListCategories returns the store's whole category tree, roots first.
func (s *Service) ListCategories(ctx context.Context) ([]domain.Category, error) {
	var out []domain.Category
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		out, err = store.ListCategories(ctx, tx)
		return err
	})
	return out, err
}

// CreateCategory adds a category, at most domain.MaxCategoryDepth deep.
func (s *Service) CreateCategory(ctx context.Context, in domain.CategoryInput) (domain.Category, error) {
	in, err := domain.ValidateCategory(in)
	if err != nil {
		return domain.Category{}, err
	}
	var c domain.Category
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		c, txErr = s.createCategoryTx(ctx, tx, in)
		return txErr
	})
	return c, err
}

func (s *Service) createCategoryTx(ctx context.Context, tx pgx.Tx, in domain.CategoryInput) (domain.Category, error) {
	n, err := store.CountCategories(ctx, tx)
	if err != nil {
		return domain.Category{}, err
	}
	if n >= domain.MaxCategoriesPerStore {
		return domain.Category{}, errCategoryLimit
	}
	if err = s.checkPlacement(ctx, tx, ulid.ID{}, in.ParentID); err != nil {
		return domain.Category{}, err
	}
	c, err := store.SaveCategory(ctx, tx, s.ids.New(), in, true)
	if err != nil {
		return domain.Category{}, err
	}
	if err = s.events.categoryChanged(ctx, tx, c.ID, domain.CategoryCreated); err != nil {
		return domain.Category{}, err
	}
	return c, s.auditCategory(ctx, tx, "category.created", c)
}

// UpdateCategory replaces a category, possibly moving it in the tree.
func (s *Service) UpdateCategory(ctx context.Context, id ulid.ID, in domain.CategoryInput) (domain.Category, error) {
	in, err := domain.ValidateCategory(in)
	if err != nil {
		return domain.Category{}, err
	}
	var c domain.Category
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		c, txErr = s.updateCategoryTx(ctx, tx, id, in)
		return txErr
	})
	return c, notFound(err, errCategoryNotFound)
}

func (s *Service) updateCategoryTx(ctx context.Context, tx pgx.Tx, id ulid.ID, in domain.CategoryInput) (domain.Category, error) {
	if _, err := store.GetCategory(ctx, tx, id); err != nil {
		return domain.Category{}, err
	}
	if err := s.checkPlacement(ctx, tx, id, in.ParentID); err != nil {
		return domain.Category{}, err
	}
	c, err := store.SaveCategory(ctx, tx, id, in, false)
	if err != nil {
		return domain.Category{}, err
	}
	if err = s.events.categoryChanged(ctx, tx, c.ID, domain.CategoryUpdated); err != nil {
		return domain.Category{}, err
	}
	return c, s.auditCategory(ctx, tx, "category.updated", c)
}

// checkPlacement validates putting category id (zero when new) under
// parent.
func (s *Service) checkPlacement(ctx context.Context, tx pgx.Tx, id ulid.ID, parent *ulid.ID) error {
	if parent == nil {
		return nil
	}
	chain, err := store.CategoryChain(ctx, tx, *parent)
	if err != nil {
		return notFound(err, store.ErrUnknownParent)
	}
	height := 1
	if !id.IsZero() {
		if height, err = store.SubtreeHeight(ctx, tx, id); err != nil {
			return err
		}
	}
	return domain.CheckPlacement(id, height, chain)
}

// DeleteCategory deletes a category that has no subcategories; its
// products simply leave it.
func (s *Service) DeleteCategory(ctx context.Context, id ulid.ID) error {
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		c, err := store.GetCategory(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := store.DeleteCategory(ctx, tx, id); err != nil {
			return err
		}
		if err := s.events.categoryChanged(ctx, tx, id, domain.CategoryDeleted); err != nil {
			return err
		}
		return s.auditCategory(ctx, tx, "category.deleted", c)
	})
	return notFound(err, errCategoryNotFound)
}

func (s *Service) auditCategory(ctx context.Context, tx pgx.Tx, action string, c domain.Category) error {
	return s.audit.RecordTx(ctx, tx, audit.Entry{Action: action, TargetType: "category", TargetID: c.ID.String(),
		Metadata: map[string]any{"name": c.Name}})
}
