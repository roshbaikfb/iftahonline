package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// SampleProduct is a product of the setup wizard's starter template
// (ADR-0034), with its one picture.
type SampleProduct struct {
	Product domain.ProductInput // its slug makes it idempotent
	// Image is the picture's bytes and type (PNG, JPEG or WebP); it goes
	// through the same re-encoding as a merchant's upload. nil: none.
	Image            []byte
	ImageContentType string
	ImageAlt         string
}

// EnsureCategory returns the category with in's slug, creating it when
// the store has none: the template may run twice.
func (s *Service) EnsureCategory(ctx context.Context, in domain.CategoryInput) (ulid.ID, error) {
	in, err := domain.ValidateCategory(in)
	if err != nil {
		return ulid.ID{}, err
	}
	var id ulid.ID
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		found, findErr := store.CategoryIDBySlug(ctx, tx, in.Slug)
		if findErr == nil {
			id = found
			return nil
		}
		if !errors.Is(findErr, domain.ErrNotFound) {
			return findErr
		}
		c, createErr := s.createCategoryTx(ctx, tx, in)
		id = c.ID
		return createErr
	})
	return id, err
}

// EnsureSampleProduct adds a sample product unless the store has a live
// product with its slug (the template may run twice, and a merchant's
// own product with that slug is left alone). The picture is stored first
// and processed like an upload, so it is checked and re-encoded too.
func (s *Service) EnsureSampleProduct(ctx context.Context, in SampleProduct) (ulid.ID, error) {
	p, err := domain.ValidateProduct(in.Product)
	if err != nil {
		return ulid.ID{}, err
	}
	var exists ulid.ID
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		exists, txErr = store.ProductIDBySlug(ctx, tx, p.Slug)
		return txErr
	})
	if err == nil {
		return exists, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return ulid.ID{}, err
	}
	storeID, err := tenant.FromContext(ctx)
	if err != nil {
		return ulid.ID{}, fmt.Errorf("catalog: %w", err)
	}
	cur, err := s.currency.StoreCurrency(ctx)
	if err != nil {
		return ulid.ID{}, fmt.Errorf("catalog: store currency: %w", err)
	}
	var imageID ulid.ID
	var key string
	if in.Image != nil {
		ct, ctErr := domain.ValidateContentType(in.ImageContentType)
		if ctErr != nil {
			return ulid.ID{}, ctErr
		}
		imageID = s.ids.New()
		key = domain.UploadKey(storeID, imageID)
		if err = s.media.Objects.Put(ctx, key, ct, in.Image); err != nil {
			return ulid.ID{}, fmt.Errorf("catalog: store sample picture: %w", err)
		}
	}
	id := s.ids.New()
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if _, txErr := s.createTx(ctx, tx, id, cur, p); txErr != nil {
			return txErr
		}
		if txErr := store.MarkSample(ctx, tx, id); txErr != nil {
			return txErr
		}
		if key == "" {
			return nil
		}
		if _, txErr := s.createImageTx(ctx, tx, id, imageID, key, domain.ImageInput{Alt: in.ImageAlt}); txErr != nil {
			return txErr
		}
		_, txErr := s.completeTx(ctx, tx, id, imageID)
		return txErr
	})
	if err != nil {
		if key != "" {
			err = errors.Join(err, s.media.Objects.Delete(ctx, key))
		}
		return ulid.ID{}, err
	}
	return id, nil
}

// DeleteSamples deletes every sample product of the store, as
// DeleteProduct does one, and returns how many.
func (s *Service) DeleteSamples(ctx context.Context) (int, error) {
	deleted := 0
	for {
		var ids []ulid.ID
		err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
			ids, txErr = store.SampleIDs(ctx, tx)
			return txErr
		})
		if err != nil || len(ids) == 0 {
			return deleted, err
		}
		for _, id := range ids {
			if err = s.DeleteProduct(ctx, id); err != nil && !errors.Is(err, ErrProductNotFound) {
				return deleted, err
			}
			if err == nil {
				deleted++
			}
		}
	}
}
