package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

// Objects is an S3-compatible bucket: S3 in production, fakes in tests.
type Objects interface {
	PresignUpload(ctx context.Context, key, contentType string, maxBytes int64, ttl time.Duration) (Upload, error)
	Get(ctx context.Context, key string, maxBytes int64) ([]byte, error)
	Put(ctx context.Context, key, contentType string, data []byte) error
	Delete(ctx context.Context, key string) error
}

var _ Objects = (*S3)(nil)

// Ingest turns the upload at uploadKey into a clean master and stores it
// at masterKey(format). It reads at most lim.MaxBytes and re-encodes with
// Process.
//
// A missing upload (code upload_missing) or a bad image is a
// *RejectError: final, see IsReject. Storage errors are returned as they
// are, for the job to retry. Deleting the upload is left to the caller,
// in the transaction that records the outcome.
func Ingest(ctx context.Context, objects Objects, lim Limits, uploadKey string, masterKey func(format string) string) (Master, string, error) {
	data, err := objects.Get(ctx, uploadKey, lim.MaxBytes)
	if errors.Is(err, ErrNoObject) {
		return Master{}, "", &RejectError{Code: "upload_missing"}
	}
	if err != nil {
		return Master{}, "", err
	}
	m, err := Process(bytes.NewReader(data), lim)
	if err != nil {
		return Master{}, "", err
	}
	key := masterKey(m.Format)
	if err = objects.Put(ctx, key, m.ContentType, m.Data); err != nil {
		return Master{}, "", err
	}
	return m, key, nil
}

// ErrForeignKey means a key is outside the store's object prefix.
var ErrForeignKey = errors.New("media: key outside the store's prefix")

// DeleteStoreObjects deletes keys of one store. If any key is outside
// store's prefix (or climbs out of it with ".."), nothing is deleted and
// the error wraps ErrForeignKey: a job can only ever delete its own
// store's objects. Missing objects are not an error.
func DeleteStoreObjects(ctx context.Context, objects Objects, store tenant.StoreID, keys []string) error {
	prefix := store.ObjectPrefix()
	for _, k := range keys {
		if !strings.HasPrefix(k, prefix) || strings.Contains(k, "..") {
			return fmt.Errorf("%w: %q is not under %s", ErrForeignKey, k, prefix)
		}
	}
	for _, k := range keys {
		if err := objects.Delete(ctx, k); err != nil {
			return err
		}
	}
	return nil
}
