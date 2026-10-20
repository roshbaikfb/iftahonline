package media

import (
	"bytes"
	"context"
	"errors"
	"time"
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
