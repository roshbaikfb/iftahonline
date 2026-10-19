package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/store/catalogdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

func imageResult(row catalogdb.ProductImage, err error, op string) (domain.Image, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Image{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Image{}, fmt.Errorf("catalog store: %s: %w", op, err)
	}
	return toImage(row), nil
}

func toImage(r catalogdb.ProductImage) domain.Image {
	img := domain.Image{
		ID: r.ID, ProductID: r.ProductID, Status: domain.ImageStatus(r.Status), UploadKey: r.UploadKey,
		ObjectKey: deref(r.ObjectKey), Format: deref(r.Format), Alt: r.Alt, Position: int(r.Position),
		FailureReason: r.FailureReason, CreatedAt: r.CreatedAt,
	}
	if r.Width != nil && r.Height != nil {
		img.Width, img.Height = int(*r.Width), int(*r.Height)
	}
	if r.Bytes != nil {
		img.Bytes = *r.Bytes
	}
	return img
}

// InsertImage creates a pending image of a product.
func InsertImage(ctx context.Context, tx Tx, productID, id ulid.ID, uploadKey string, in domain.ImageInput) (domain.Image, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Image{}, err
	}
	row, err := catalogdb.New(tx).InsertImage(ctx, catalogdb.InsertImageParams{
		ID: id, StoreID: store, ProductID: productID, UploadKey: uploadKey, Alt: in.Alt, Position: int32(in.Position), //nolint:gosec // bounded by domain.MaxImagePosition
	})
	return imageResult(row, err, "insert image")
}

// CountImages counts a product's images, pending ones included.
func CountImages(ctx context.Context, tx Tx, productID ulid.ID) (int, error) {
	store, err := current(ctx)
	if err != nil {
		return 0, err
	}
	n, err := catalogdb.New(tx).CountProductImages(ctx, catalogdb.CountProductImagesParams{StoreID: store, ProductID: productID})
	if err != nil {
		return 0, fmt.Errorf("catalog store: count images: %w", err)
	}
	return int(n), nil
}

// GetImage returns one image of a product.
func GetImage(ctx context.Context, tx Tx, productID, id ulid.ID) (domain.Image, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Image{}, err
	}
	row, err := catalogdb.New(tx).GetImage(ctx, catalogdb.GetImageParams{StoreID: store, ProductID: productID, ID: id})
	return imageResult(row, err, "get image")
}

// GetImageByID returns an image by ID alone (jobs).
func GetImageByID(ctx context.Context, tx Tx, id ulid.ID) (domain.Image, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Image{}, err
	}
	row, err := catalogdb.New(tx).GetImageByID(ctx, catalogdb.GetImageByIDParams{StoreID: store, ID: id})
	return imageResult(row, err, "get image")
}

// StartProcessing moves a pending image to processing; ErrNotFound when
// it is not pending.
func StartProcessing(ctx context.Context, tx Tx, productID, id ulid.ID) (domain.Image, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Image{}, err
	}
	row, err := catalogdb.New(tx).StartImageProcessing(ctx, catalogdb.StartImageProcessingParams{StoreID: store, ProductID: productID, ID: id})
	return imageResult(row, err, "start processing")
}

// Ready describes a stored master.
type Ready struct {
	ObjectKey     string
	Format        string
	Width, Height int
	Bytes         int64
}

// MarkReady records a processing image's master; ErrNotFound when it is
// not processing (deleted meanwhile, or already done).
func MarkReady(ctx context.Context, tx Tx, id ulid.ID, r Ready) (domain.Image, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Image{}, err
	}
	w, h := int32(r.Width), int32(r.Height) //nolint:gosec // bounded by media.MaxSide
	row, err := catalogdb.New(tx).MarkImageReady(ctx, catalogdb.MarkImageReadyParams{
		StoreID: store, ID: id, ObjectKey: &r.ObjectKey, Format: &r.Format, Width: &w, Height: &h, Bytes: &r.Bytes,
	})
	return imageResult(row, err, "mark ready")
}

// MarkFailed rejects a processing image.
func MarkFailed(ctx context.Context, tx Tx, id ulid.ID, reason string) (domain.Image, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Image{}, err
	}
	row, err := catalogdb.New(tx).MarkImageFailed(ctx, catalogdb.MarkImageFailedParams{StoreID: store, ID: id, FailureReason: reason})
	return imageResult(row, err, "mark failed")
}

// UpdateImage sets alt text and position.
func UpdateImage(ctx context.Context, tx Tx, productID, id ulid.ID, in domain.ImageInput) (domain.Image, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Image{}, err
	}
	row, err := catalogdb.New(tx).UpdateImage(ctx, catalogdb.UpdateImageParams{
		StoreID: store, ProductID: productID, ID: id, Alt: in.Alt, Position: int32(in.Position), //nolint:gosec // bounded by domain.MaxImagePosition
	})
	return imageResult(row, err, "update image")
}

func keys(upload string, object *string) []string {
	out := []string{upload}
	if object != nil {
		out = append(out, *object)
	}
	return out
}

// DeleteImage deletes an image row and returns its object keys.
func DeleteImage(ctx context.Context, tx Tx, productID, id ulid.ID) ([]string, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	row, err := catalogdb.New(tx).DeleteImage(ctx, catalogdb.DeleteImageParams{StoreID: store, ProductID: productID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("catalog store: delete image: %w", err)
	}
	return keys(row.UploadKey, row.ObjectKey), nil
}

// DeletePendingImage deletes an image still waiting for its upload and
// returns its upload key; ErrNotFound when it is no longer pending.
func DeletePendingImage(ctx context.Context, tx Tx, id ulid.ID) (string, error) {
	store, err := current(ctx)
	if err != nil {
		return "", err
	}
	key, err := catalogdb.New(tx).DeletePendingImage(ctx, catalogdb.DeletePendingImageParams{StoreID: store, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("catalog store: delete pending image: %w", err)
	}
	return key, nil
}

// DeleteProductImages deletes every image row of a product and returns
// their object keys.
func DeleteProductImages(ctx context.Context, tx Tx, productID ulid.ID) ([]string, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := catalogdb.New(tx).DeleteProductImages(ctx, catalogdb.DeleteProductImagesParams{StoreID: store, ProductID: productID})
	if err != nil {
		return nil, fmt.Errorf("catalog store: delete product images: %w", err)
	}
	var out []string
	for _, r := range rows {
		out = append(out, keys(r.UploadKey, r.ObjectKey)...)
	}
	return out, nil
}

// loadImages attaches images to a page of products with one query.
func loadImages(ctx context.Context, tx Tx, store ulid.ID, ids []ulid.ID, out []domain.Product, index map[ulid.ID]int) error {
	rows, err := catalogdb.New(tx).ListImagesByProducts(ctx, catalogdb.ListImagesByProductsParams{StoreID: store, ProductIds: ids})
	if err != nil {
		return fmt.Errorf("catalog store: list images: %w", err)
	}
	for _, r := range rows {
		p := &out[index[r.ProductID]]
		p.Images = append(p.Images, toImage(r))
	}
	return nil
}
