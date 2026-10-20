package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/media"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// QueueMedia is the River queue for image jobs. Its own (small)
// concurrency bounds how many decoded images a worker holds at once.
const QueueMedia = "media"

// ObjectStore is the S3-compatible bucket (media.S3 in production).
type ObjectStore interface {
	PresignUpload(ctx context.Context, key, contentType string, maxBytes int64, ttl time.Duration) (media.Upload, error)
	Get(ctx context.Context, key string, maxBytes int64) ([]byte, error)
	Put(ctx context.Context, key, contentType string, data []byte) error
	Delete(ctx context.Context, key string) error
}

// Media is the service's object storage and the limits on originals.
type Media struct {
	Objects ObjectStore
	Limits  media.Limits
}

var (
	errImageNotFound = errs.NotFound("image_not_found", "image not found")
	errImageLimit    = errs.Conflict("image_limit", fmt.Sprintf("a product can have at most %d images", domain.MaxProductImages))
	errImageFailed   = errs.Conflict("image_failed", "the image was rejected; delete it and upload again")
)

// CreateImageUpload adds a pending image to a product and returns a
// presigned upload for the browser. The storage service enforces the
// declared type and the size limit; the job checks the real content.
func (s *Service) CreateImageUpload(ctx context.Context, productID ulid.ID, contentType string, in domain.ImageInput) (domain.Image, media.Upload, error) {
	ct, ctErr := domain.ValidateContentType(contentType)
	in, inErr := domain.ValidateImageInput(in)
	if err := errors.Join(ctErr, inErr); err != nil {
		return domain.Image{}, media.Upload{}, mergeValidation(ctErr, inErr)
	}
	storeID, err := tenant.FromContext(ctx)
	if err != nil {
		return domain.Image{}, media.Upload{}, fmt.Errorf("catalog: %w", err)
	}
	id := s.ids.New()
	key := domain.UploadKey(storeID, id)
	var img domain.Image
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		img, txErr = s.createImageTx(ctx, tx, productID, id, key, in)
		return txErr
	})
	if err != nil {
		return domain.Image{}, media.Upload{}, notFound(err, ErrProductNotFound)
	}
	// Presigning is a local signature, not a network call.
	up, err := s.media.Objects.PresignUpload(ctx, key, ct, s.media.Limits.MaxBytes, domain.UploadURLTTL)
	if err != nil {
		return domain.Image{}, media.Upload{}, err
	}
	return img, up, nil
}

func (s *Service) createImageTx(ctx context.Context, tx pgx.Tx, productID, id ulid.ID, key string, in domain.ImageInput) (domain.Image, error) {
	if err := store.LockProduct(ctx, tx, productID); err != nil {
		return domain.Image{}, err
	}
	n, err := store.CountImages(ctx, tx, productID)
	if err != nil {
		return domain.Image{}, err
	}
	if n >= domain.MaxProductImages {
		return domain.Image{}, errImageLimit
	}
	img, err := store.InsertImage(ctx, tx, productID, id, key, in)
	if err != nil {
		return domain.Image{}, err
	}
	if err = s.enqueue(ctx, tx, func(t jobs.TenantArgs) river.JobArgs { return ExpireUploadArgs{TenantArgs: t, ImageID: id} },
		&river.InsertOpts{ScheduledAt: s.now().Add(domain.PendingUploadTTL)}); err != nil {
		return domain.Image{}, err
	}
	return img, s.auditImage(ctx, tx, "image.created", img)
}

// mergeValidation joins the field errors of several validation errors.
func mergeValidation(list ...error) error {
	var fields []errs.FieldError
	for _, err := range list {
		var e *errs.Error
		if errors.As(err, &e) {
			fields = append(fields, e.Fields...)
		}
	}
	return errs.Validation(fields...)
}

// CompleteImageUpload starts processing an uploaded image. Repeating it
// is harmless.
func (s *Service) CompleteImageUpload(ctx context.Context, productID, imageID ulid.ID) (domain.Image, error) {
	var img domain.Image
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		img, txErr = s.completeTx(ctx, tx, productID, imageID)
		return txErr
	})
	return img, notFound(err, errImageNotFound)
}

func (s *Service) completeTx(ctx context.Context, tx pgx.Tx, productID, imageID ulid.ID) (domain.Image, error) {
	img, err := store.StartProcessing(ctx, tx, productID, imageID)
	if errors.Is(err, domain.ErrNotFound) {
		cur, getErr := store.GetImage(ctx, tx, productID, imageID)
		switch {
		case getErr != nil:
			return domain.Image{}, getErr
		case cur.Status == domain.ImageFailed:
			return domain.Image{}, errImageFailed
		}
		return cur, nil // already processing or ready
	}
	if err != nil {
		return domain.Image{}, err
	}
	return img, s.enqueue(ctx, tx, func(t jobs.TenantArgs) river.JobArgs { return ProcessImageArgs{TenantArgs: t, ImageID: imageID} }, nil)
}

// UpdateImage sets an image's alt text and position.
func (s *Service) UpdateImage(ctx context.Context, productID, imageID ulid.ID, in domain.ImageInput) (domain.Image, error) {
	in, err := domain.ValidateImageInput(in)
	if err != nil {
		return domain.Image{}, err
	}
	var img domain.Image
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		if img, txErr = store.UpdateImage(ctx, tx, productID, imageID, in); txErr != nil {
			return txErr
		}
		if txErr = s.changed(ctx, tx, productID, domain.ReasonImages); txErr != nil {
			return txErr
		}
		return s.auditImage(ctx, tx, "image.updated", img)
	})
	return img, notFound(err, errImageNotFound)
}

// DeleteImage deletes an image; its objects are removed by a job.
func (s *Service) DeleteImage(ctx context.Context, productID, imageID ulid.ID) error {
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		keys, err := store.DeleteImage(ctx, tx, productID, imageID)
		if err != nil {
			return err
		}
		if err = s.deleteObjects(ctx, tx, keys); err != nil {
			return err
		}
		if err = s.changed(ctx, tx, productID, domain.ReasonImages); err != nil {
			return err
		}
		return s.auditImage(ctx, tx, "image.deleted", domain.Image{ID: imageID})
	})
	return notFound(err, errImageNotFound)
}

func (s *Service) deleteObjects(ctx context.Context, tx pgx.Tx, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	return s.enqueue(ctx, tx, func(t jobs.TenantArgs) river.JobArgs { return DeleteObjectsArgs{TenantArgs: t, Keys: keys} }, nil)
}

// enqueue inserts a tenant job built by mk in tx (transactional outbox).
func (s *Service) enqueue(ctx context.Context, tx pgx.Tx, mk func(jobs.TenantArgs) river.JobArgs, opts *river.InsertOpts) error {
	return enqueueTx(ctx, s.jobs, tx, mk, opts)
}

func enqueueTx(ctx context.Context, ins jobs.Inserter, tx pgx.Tx, mk func(jobs.TenantArgs) river.JobArgs, opts *river.InsertOpts) error {
	t, err := jobs.ForTenant(ctx)
	if err != nil {
		return err
	}
	args := mk(t)
	if _, err = ins.InsertTx(ctx, tx, args, opts); err != nil {
		return fmt.Errorf("catalog: enqueue %s: %w", args.Kind(), err)
	}
	return nil
}

func (s *Service) auditImage(ctx context.Context, tx pgx.Tx, action string, img domain.Image) error {
	return s.audit.RecordTx(ctx, tx, auditEntry(action, "image", img.ID))
}

// ProcessImageArgs validates and re-encodes an uploaded original.
type ProcessImageArgs struct {
	jobs.TenantArgs
	ImageID ulid.ID `json:"image_id"`
}

// Kind implements river.JobArgs.
func (ProcessImageArgs) Kind() string { return "catalog.process_image" }

// InsertOpts puts image jobs on the media queue.
func (ProcessImageArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{Queue: QueueMedia} }

// ExpireUploadArgs removes an image whose upload never completed.
type ExpireUploadArgs struct {
	jobs.TenantArgs
	ImageID ulid.ID `json:"image_id"`
}

// Kind implements river.JobArgs.
func (ExpireUploadArgs) Kind() string { return "catalog.expire_image_upload" }

// DeleteObjectsArgs removes objects of the store from the bucket.
type DeleteObjectsArgs struct {
	jobs.TenantArgs
	Keys []string `json:"keys"`
}

// Kind implements river.JobArgs.
func (DeleteObjectsArgs) Kind() string { return "catalog.delete_objects" }

// ImageWorkers run the image jobs.
type ImageWorkers struct {
	DB     postgres.Beginner
	Jobs   jobs.Inserter
	Media  Media
	Events Events
}

// ProcessWorker runs ProcessImageArgs.
type ProcessWorker struct {
	river.WorkerDefaults[ProcessImageArgs]
	*ImageWorkers
}

// Timeout bounds one image: download, decode, encode, upload.
func (*ProcessWorker) Timeout(*river.Job[ProcessImageArgs]) time.Duration { return 2 * time.Minute }

// Work turns the original into a master, or rejects it. Storage errors
// are retried; a bad upload is a final "failed" with a reason code.
func (w *ProcessWorker) Work(ctx context.Context, job *river.Job[ProcessImageArgs]) error {
	storeID, err := tenant.FromContext(ctx)
	if err != nil {
		return err
	}
	var img domain.Image
	if err = postgres.InTenantTx(ctx, w.DB, func(tx pgx.Tx) (txErr error) {
		img, txErr = store.GetImageByID(ctx, tx, job.Args.ImageID)
		return txErr
	}); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil // deleted meanwhile
		}
		return err
	}
	if img.Status != domain.ImageProcessing {
		return nil
	}
	data, err := w.Media.Objects.Get(ctx, img.UploadKey, w.Media.Limits.MaxBytes)
	if errors.Is(err, media.ErrNoObject) {
		return w.fail(ctx, img, "upload_missing")
	}
	if err != nil {
		return err
	}
	m, err := media.Process(bytes.NewReader(data), w.Media.Limits)
	if code, rejected := media.IsReject(err); rejected {
		return w.fail(ctx, img, code)
	}
	if err != nil {
		return err
	}
	key := domain.MasterKey(storeID, img.ID, m.Format)
	if err = w.Media.Objects.Put(ctx, key, m.ContentType, m.Data); err != nil {
		return err
	}
	return postgres.InTenantTx(ctx, w.DB, func(tx pgx.Tx) error {
		_, markErr := store.MarkReady(ctx, tx, img.ID, store.Ready{ObjectKey: key, Format: m.Format, Width: m.Width, Height: m.Height, Bytes: int64(len(m.Data))})
		garbage := []string{img.UploadKey}
		switch {
		case errors.Is(markErr, domain.ErrNotFound):
			garbage = append(garbage, key) // deleted while we worked
		case markErr != nil:
			return markErr
		default:
			if err := reindexTx(ctx, w.Jobs, tx, img.ProductID); err != nil {
				return err
			}
			if err := w.Events.productChanged(ctx, tx, img.ProductID, domain.ReasonImages); err != nil {
				return err
			}
		}
		return enqueueTx(ctx, w.Jobs, tx, func(t jobs.TenantArgs) river.JobArgs { return DeleteObjectsArgs{TenantArgs: t, Keys: garbage} }, nil)
	})
}

func (w *ProcessWorker) fail(ctx context.Context, img domain.Image, code string) error {
	return postgres.InTenantTx(ctx, w.DB, func(tx pgx.Tx) error {
		if _, err := store.MarkFailed(ctx, tx, img.ID, code); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		return enqueueTx(ctx, w.Jobs, tx, func(t jobs.TenantArgs) river.JobArgs {
			return DeleteObjectsArgs{TenantArgs: t, Keys: []string{img.UploadKey}}
		}, nil)
	})
}

// ExpireUploadWorker runs ExpireUploadArgs.
type ExpireUploadWorker struct {
	river.WorkerDefaults[ExpireUploadArgs]
	*ImageWorkers
}

// Work deletes the image if it is still pending, and any partial upload.
func (w *ExpireUploadWorker) Work(ctx context.Context, job *river.Job[ExpireUploadArgs]) error {
	return postgres.InTenantTx(ctx, w.DB, func(tx pgx.Tx) error {
		key, err := store.DeletePendingImage(ctx, tx, job.Args.ImageID)
		if errors.Is(err, domain.ErrNotFound) {
			return nil // completed (or deleted) in time
		}
		if err != nil {
			return err
		}
		return enqueueTx(ctx, w.Jobs, tx, func(t jobs.TenantArgs) river.JobArgs { return DeleteObjectsArgs{TenantArgs: t, Keys: []string{key}} }, nil)
	})
}

// DeleteObjectsWorker runs DeleteObjectsArgs.
type DeleteObjectsWorker struct {
	river.WorkerDefaults[DeleteObjectsArgs]
	*ImageWorkers
}

// Work deletes the keys. A key outside the job's store prefix is refused:
// a job can only ever delete its own store's objects.
func (w *DeleteObjectsWorker) Work(ctx context.Context, job *river.Job[DeleteObjectsArgs]) error {
	storeID, err := tenant.FromContext(ctx)
	if err != nil {
		return err
	}
	prefix := domain.StorePrefix(storeID)
	for _, k := range job.Args.Keys {
		if !strings.HasPrefix(k, prefix) || strings.Contains(k, "..") {
			return river.JobCancel(fmt.Errorf("catalog: refusing to delete %q outside %s", k, prefix))
		}
	}
	for _, k := range job.Args.Keys {
		if err := w.Media.Objects.Delete(ctx, k); err != nil {
			return err
		}
	}
	return nil
}
