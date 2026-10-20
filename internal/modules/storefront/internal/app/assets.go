package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/event"
	"github.com/roshbaikfb/iftahonline/internal/platform/eventbus"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/media"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// QueueMedia is the River queue for image jobs, shared with the catalog
// so one concurrency limit bounds the decoded images a worker holds.
const QueueMedia = "media"

// Media is object storage and the limits on originals.
type Media struct {
	Objects media.Objects
	Limits  media.Limits
}

// Errors.
var (
	ErrAssetNotFound = errs.NotFound("asset_not_found", "logo upload not found")
	ErrNoLogo        = errs.NotFound("logo_not_found", "the store has no logo")
	errAssetFailed   = errs.Conflict("asset_failed", "the image was rejected; upload another one")
)

// Logo is the store's live logo and its newest unfinished upload; either
// may be nil.
type Logo struct {
	Current *domain.Asset
	Latest  *domain.Asset
}

// GetLogo returns the logo and any upload in progress.
func (s *Service) GetLogo(ctx context.Context) (Logo, error) {
	var l Logo
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		l.Current, l.Latest, txErr = store.CurrentAssets(ctx, tx, domain.AssetLogo)
		return txErr
	})
	return l, err
}

// CreateLogoUpload starts a logo upload: a pending asset and a presigned
// upload for the browser. Earlier unfinished uploads are abandoned; the
// live logo stays until the new one is ready.
func (s *Service) CreateLogoUpload(ctx context.Context, contentType string) (domain.Asset, media.Upload, error) {
	ct, err := domain.ValidateLogoContentType(contentType)
	if err != nil {
		return domain.Asset{}, media.Upload{}, err
	}
	storeID, err := tenant.FromContext(ctx)
	if err != nil {
		return domain.Asset{}, media.Upload{}, fmt.Errorf("storefront: %w", err)
	}
	id := s.ids.New()
	key := domain.AssetUploadKey(storeID, id)
	var a domain.Asset
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		abandoned, txErr := store.DeleteUnreadyAssets(ctx, tx, domain.AssetLogo)
		if txErr != nil {
			return txErr
		}
		if txErr = s.deleteObjects(ctx, tx, abandoned...); txErr != nil {
			return txErr
		}
		if a, txErr = store.InsertAsset(ctx, tx, id, domain.AssetLogo, key); txErr != nil {
			return txErr
		}
		if txErr = s.enqueue(ctx, tx, func(t jobs.TenantArgs) river.JobArgs { return ExpireAssetUploadArgs{TenantArgs: t, AssetID: id} },
			&river.InsertOpts{ScheduledAt: time.Now().Add(domain.PendingAssetTTL)}); txErr != nil {
			return txErr
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "logo.uploaded", TargetType: "asset", TargetID: id.String()})
	})
	if err != nil {
		return domain.Asset{}, media.Upload{}, err
	}
	// Presigning is a local signature, not a network call.
	up, err := s.media.Objects.PresignUpload(ctx, key, ct, min(s.media.Limits.MaxBytes, domain.MaxLogoBytes), domain.AssetUploadTTL)
	if err != nil {
		return domain.Asset{}, media.Upload{}, err
	}
	return a, up, nil
}

// CompleteLogoUpload hands an uploaded logo to the processing job. It is
// idempotent while the asset is processing or ready.
func (s *Service) CompleteLogoUpload(ctx context.Context, id ulid.ID) (domain.Asset, error) {
	var a domain.Asset
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		a, txErr = store.StartAssetProcessing(ctx, tx, id)
		if errors.Is(txErr, domain.ErrNotFound) {
			cur, getErr := store.GetAsset(ctx, tx, id)
			switch {
			case errors.Is(getErr, domain.ErrNotFound):
				return ErrAssetNotFound
			case getErr != nil:
				return getErr
			case cur.Status == domain.AssetFailed:
				return errAssetFailed
			}
			a = cur // already processing or ready
			return nil
		}
		if txErr != nil {
			return txErr
		}
		return s.enqueue(ctx, tx, func(t jobs.TenantArgs) river.JobArgs { return ProcessAssetArgs{TenantArgs: t, AssetID: id} }, nil)
	})
	return a, err
}

// DeleteLogo removes the live logo.
func (s *Service) DeleteLogo(ctx context.Context) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		key, err := store.DeleteReadyAsset(ctx, tx, domain.AssetLogo)
		if errors.Is(err, domain.ErrNotFound) {
			return ErrNoLogo
		}
		if err != nil {
			return err
		}
		if err = s.deleteObjects(ctx, tx, key); err != nil {
			return err
		}
		if err = themeChanged(ctx, tx, s.events, s.ids); err != nil {
			return err
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "logo.deleted", TargetType: "asset"})
	})
}

func (s *Service) enqueue(ctx context.Context, tx pgx.Tx, mk func(jobs.TenantArgs) river.JobArgs, opts *river.InsertOpts) error {
	return enqueueTx(ctx, s.jobs, tx, mk, opts)
}

func (s *Service) deleteObjects(ctx context.Context, tx pgx.Tx, keys ...string) error {
	return deleteObjectsTx(ctx, s.jobs, tx, keys...)
}

func enqueueTx(ctx context.Context, ins jobs.Inserter, tx pgx.Tx, mk func(jobs.TenantArgs) river.JobArgs, opts *river.InsertOpts) error {
	t, err := jobs.ForTenant(ctx)
	if err != nil {
		return err
	}
	args := mk(t)
	if _, err = ins.InsertTx(ctx, tx, args, opts); err != nil {
		return fmt.Errorf("storefront: enqueue %s: %w", args.Kind(), err)
	}
	return nil
}

func deleteObjectsTx(ctx context.Context, ins jobs.Inserter, tx pgx.Tx, keys ...string) error {
	var live []string
	for _, k := range keys {
		if k != "" {
			live = append(live, k)
		}
	}
	if len(live) == 0 {
		return nil
	}
	return enqueueTx(ctx, ins, tx, func(t jobs.TenantArgs) river.JobArgs { return DeleteObjectsArgs{TenantArgs: t, Keys: live} }, nil)
}

func themeChanged(ctx context.Context, tx pgx.Tx, pub eventbus.Publisher, ids *ulid.Generator) error {
	m, err := event.MetaFor(ctx, ids, domain.EventThemeChanged, 1)
	if err != nil {
		return err
	}
	return pub.PublishTx(ctx, tx, domain.ThemeChanged{Meta: m})
}

// ProcessAssetArgs validates and re-encodes an uploaded asset.
type ProcessAssetArgs struct {
	jobs.TenantArgs
	AssetID ulid.ID `json:"asset_id"`
}

// Kind implements river.JobArgs.
func (ProcessAssetArgs) Kind() string { return "storefront.process_asset" }

// InsertOpts puts the job on the media queue.
func (ProcessAssetArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{Queue: QueueMedia} }

// ExpireAssetUploadArgs removes an asset whose upload never came.
type ExpireAssetUploadArgs struct {
	jobs.TenantArgs
	AssetID ulid.ID `json:"asset_id"`
}

// Kind implements river.JobArgs.
func (ExpireAssetUploadArgs) Kind() string { return "storefront.expire_asset_upload" }

// DeleteObjectsArgs removes objects of the store from the bucket.
type DeleteObjectsArgs struct {
	jobs.TenantArgs
	Keys []string `json:"keys"`
}

// Kind implements river.JobArgs.
func (DeleteObjectsArgs) Kind() string { return "storefront.delete_objects" }

// AssetWorkers run the asset jobs.
type AssetWorkers struct {
	DB     postgres.Beginner
	Jobs   jobs.Inserter
	Media  Media
	Events eventbus.Publisher
	IDs    *ulid.Generator
}

// ProcessAssetWorker runs ProcessAssetArgs.
type ProcessAssetWorker struct {
	river.WorkerDefaults[ProcessAssetArgs]
	*AssetWorkers
}

// Timeout bounds one image: download, decode, encode, upload.
func (*ProcessAssetWorker) Timeout(*river.Job[ProcessAssetArgs]) time.Duration {
	return 2 * time.Minute
}

// Work turns the upload into a master and makes it the live asset of its
// kind, replacing the previous one; a bad upload is a final "failed".
// Storage errors are retried.
func (w *ProcessAssetWorker) Work(ctx context.Context, job *river.Job[ProcessAssetArgs]) error {
	storeID, err := tenant.FromContext(ctx)
	if err != nil {
		return err
	}
	var a domain.Asset
	if err = postgres.InTenantTx(ctx, w.DB, func(tx pgx.Tx) (txErr error) {
		a, txErr = store.GetAsset(ctx, tx, job.Args.AssetID)
		return txErr
	}); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil // abandoned meanwhile
		}
		return err
	}
	if a.Status != domain.AssetProcessing {
		return nil
	}
	m, key, err := media.Ingest(ctx, w.Media.Objects, w.Media.Limits, a.UploadKey,
		func(format string) string { return domain.AssetMasterKey(storeID, a.ID, format) })
	if code, rejected := media.IsReject(err); rejected {
		return postgres.InTenantTx(ctx, w.DB, func(tx pgx.Tx) error {
			if markErr := store.MarkAssetFailed(ctx, tx, a.ID, code); markErr != nil {
				return markErr
			}
			return deleteObjectsTx(ctx, w.Jobs, tx, a.UploadKey)
		})
	}
	if err != nil {
		return err
	}
	return postgres.InTenantTx(ctx, w.DB, func(tx pgx.Tx) error {
		garbage := []string{a.UploadKey}
		old, delErr := store.DeleteReadyAsset(ctx, tx, a.Kind)
		switch {
		case errors.Is(delErr, domain.ErrNotFound):
		case delErr != nil:
			return delErr
		default:
			garbage = append(garbage, old)
		}
		_, markErr := store.MarkAssetReady(ctx, tx, a.ID, store.AssetMaster{
			ObjectKey: key, Format: m.Format, Width: m.Width, Height: m.Height, Bytes: int64(len(m.Data)),
		})
		if errors.Is(markErr, domain.ErrNotFound) {
			return deleteObjectsTx(ctx, w.Jobs, tx, a.UploadKey, key) // abandoned while we worked; keep the old logo
		}
		if markErr != nil {
			return markErr
		}
		if err := deleteObjectsTx(ctx, w.Jobs, tx, garbage...); err != nil {
			return err
		}
		return themeChanged(ctx, tx, w.Events, w.IDs)
	})
}

// ExpireAssetUploadWorker runs ExpireAssetUploadArgs.
type ExpireAssetUploadWorker struct {
	river.WorkerDefaults[ExpireAssetUploadArgs]
	*AssetWorkers
}

// Work deletes the asset if it is still pending, and any partial upload.
func (w *ExpireAssetUploadWorker) Work(ctx context.Context, job *river.Job[ExpireAssetUploadArgs]) error {
	return postgres.InTenantTx(ctx, w.DB, func(tx pgx.Tx) error {
		key, err := store.DeletePendingAsset(ctx, tx, job.Args.AssetID)
		if errors.Is(err, domain.ErrNotFound) {
			return nil // completed or abandoned in time
		}
		if err != nil {
			return err
		}
		return deleteObjectsTx(ctx, w.Jobs, tx, key)
	})
}

// DeleteObjectsWorker runs DeleteObjectsArgs.
type DeleteObjectsWorker struct {
	river.WorkerDefaults[DeleteObjectsArgs]
	*AssetWorkers
}

// Work deletes the keys; a key outside the job's store is refused.
func (w *DeleteObjectsWorker) Work(ctx context.Context, job *river.Job[DeleteObjectsArgs]) error {
	storeID, err := tenant.FromContext(ctx)
	if err != nil {
		return err
	}
	err = media.DeleteStoreObjects(ctx, w.Media.Objects, storeID, job.Args.Keys)
	if errors.Is(err, media.ErrForeignKey) {
		return river.JobCancel(fmt.Errorf("storefront: %w", err))
	}
	return err
}
