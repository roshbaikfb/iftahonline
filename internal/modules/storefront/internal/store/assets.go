package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/store/storefrontdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// InsertAsset creates a pending asset awaiting its upload.
func InsertAsset(ctx context.Context, tx Tx, id ulid.ID, kind domain.AssetKind, uploadKey string) (domain.Asset, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Asset{}, err
	}
	row, err := storefrontdb.New(tx).InsertAsset(ctx, storefrontdb.InsertAssetParams{ID: id, StoreID: store, Kind: string(kind), UploadKey: uploadKey})
	if err != nil {
		return domain.Asset{}, fmt.Errorf("storefront store: insert asset: %w", err)
	}
	return toAsset(row), nil
}

// GetAsset returns an asset, or domain.ErrNotFound.
func GetAsset(ctx context.Context, tx Tx, id ulid.ID) (domain.Asset, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Asset{}, err
	}
	row, err := storefrontdb.New(tx).GetAsset(ctx, storefrontdb.GetAssetParams{StoreID: store, ID: id})
	if err != nil {
		return domain.Asset{}, notFound(err, "get asset")
	}
	return toAsset(row), nil
}

// CurrentAssets returns the live asset of a kind and the newest upload
// that is not live yet; either may be nil.
func CurrentAssets(ctx context.Context, tx Tx, kind domain.AssetKind) (ready, latest *domain.Asset, err error) {
	store, err := current(ctx)
	if err != nil {
		return nil, nil, err
	}
	q := storefrontdb.New(tx)
	r, err := q.GetReadyAsset(ctx, storefrontdb.GetReadyAssetParams{StoreID: store, Kind: string(kind)})
	switch {
	case err == nil:
		a := toAsset(r)
		ready = &a
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, nil, fmt.Errorf("storefront store: get ready asset: %w", err)
	}
	l, err := q.GetLatestUnreadyAsset(ctx, storefrontdb.GetLatestUnreadyAssetParams{StoreID: store, Kind: string(kind)})
	switch {
	case err == nil:
		a := toAsset(l)
		latest = &a
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, nil, fmt.Errorf("storefront store: get latest asset: %w", err)
	}
	return ready, latest, nil
}

// DeleteUnreadyAssets removes a kind's unfinished uploads and returns
// their upload keys, for the objects to be deleted.
func DeleteUnreadyAssets(ctx context.Context, tx Tx, kind domain.AssetKind) ([]string, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	keys, err := storefrontdb.New(tx).DeleteUnreadyAssets(ctx, storefrontdb.DeleteUnreadyAssetsParams{StoreID: store, Kind: string(kind)})
	if err != nil {
		return nil, fmt.Errorf("storefront store: delete unready assets: %w", err)
	}
	return keys, nil
}

// StartAssetProcessing moves a pending asset to processing, or returns
// domain.ErrNotFound if it is not pending.
func StartAssetProcessing(ctx context.Context, tx Tx, id ulid.ID) (domain.Asset, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Asset{}, err
	}
	row, err := storefrontdb.New(tx).StartAssetProcessing(ctx, storefrontdb.StartAssetProcessingParams{StoreID: store, ID: id})
	if err != nil {
		return domain.Asset{}, notFound(err, "start asset processing")
	}
	return toAsset(row), nil
}

// DeleteReadyAsset removes the live asset of a kind and returns its
// master's key, or domain.ErrNotFound when there is none.
func DeleteReadyAsset(ctx context.Context, tx Tx, kind domain.AssetKind) (string, error) {
	store, err := current(ctx)
	if err != nil {
		return "", err
	}
	key, err := storefrontdb.New(tx).DeleteReadyAsset(ctx, storefrontdb.DeleteReadyAssetParams{StoreID: store, Kind: string(kind)})
	if err != nil {
		return "", notFound(err, "delete ready asset")
	}
	return deref(key), nil
}

// AssetMaster is a processed master to record.
type AssetMaster struct {
	ObjectKey string
	Format    string
	Width     int
	Height    int
	Bytes     int64
}

// MarkAssetReady records a processing asset's master, or returns
// domain.ErrNotFound if it is no longer processing.
func MarkAssetReady(ctx context.Context, tx Tx, id ulid.ID, m AssetMaster) (domain.Asset, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Asset{}, err
	}
	w, h := int32(m.Width), int32(m.Height) //nolint:gosec // bounded by media.Limits.MaxPixels
	row, err := storefrontdb.New(tx).MarkAssetReady(ctx, storefrontdb.MarkAssetReadyParams{
		StoreID: store, ID: id, ObjectKey: &m.ObjectKey, Format: &m.Format, Width: &w, Height: &h, Bytes: &m.Bytes,
	})
	if err != nil {
		return domain.Asset{}, notFound(err, "mark asset ready")
	}
	return toAsset(row), nil
}

// MarkAssetFailed records why a processing asset was rejected; an asset
// that is no longer processing is left alone.
func MarkAssetFailed(ctx context.Context, tx Tx, id ulid.ID, reason string) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	_, err = storefrontdb.New(tx).MarkAssetFailed(ctx, storefrontdb.MarkAssetFailedParams{StoreID: store, ID: id, FailureReason: reason})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("storefront store: mark asset failed: %w", err)
	}
	return nil
}

// DeletePendingAsset removes an asset still waiting for its upload and
// returns the upload key, or domain.ErrNotFound.
func DeletePendingAsset(ctx context.Context, tx Tx, id ulid.ID) (string, error) {
	store, err := current(ctx)
	if err != nil {
		return "", err
	}
	key, err := storefrontdb.New(tx).DeletePendingAsset(ctx, storefrontdb.DeletePendingAssetParams{StoreID: store, ID: id})
	if err != nil {
		return "", notFound(err, "delete pending asset")
	}
	return key, nil
}

func toAsset(r storefrontdb.StoreAsset) domain.Asset {
	a := domain.Asset{
		ID: r.ID, Kind: domain.AssetKind(r.Kind), Status: domain.AssetStatus(r.Status), UploadKey: r.UploadKey,
		ObjectKey: deref(r.ObjectKey), Format: deref(r.Format), FailureReason: r.FailureReason, CreatedAt: r.CreatedAt,
	}
	if r.Width != nil && r.Height != nil {
		a.Width, a.Height = int(*r.Width), int(*r.Height)
	}
	if r.Bytes != nil {
		a.Bytes = *r.Bytes
	}
	return a
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
