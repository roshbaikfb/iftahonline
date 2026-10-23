package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/config"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ErrNoObject means the key does not exist (the browser never uploaded).
var ErrNoObject = errors.New("media: no such object")

// S3Config locates the bucket.
type S3Config struct {
	Endpoint       string // used by the servers
	PublicEndpoint string // used in presigned uploads; browsers must reach it
	Region         string
	Bucket         string
	AccessKey      string
	SecretKey      string
	// Timeout bounds each call (minio-go retries with backoff inside it).
	Timeout time.Duration
}

// Upload is a presigned browser upload: a multipart/form-data POST of
// Fields plus the file (as the last field, named "file") to URL.
type Upload struct {
	URL       string
	Fields    map[string]string
	ExpiresAt time.Time
	MaxBytes  int64
}

// S3 stores objects in an S3-compatible bucket.
type S3 struct {
	cfg    S3Config
	client *minio.Client // servers
	public *minio.Client // presigning only: it never sends a request
}

// NewS3 builds the clients. Path-style addressing works with every
// S3-compatible store (SeaweedFS, MinIO, R2, AWS).
func NewS3(c S3Config) (*S3, error) {
	if c.Timeout <= 0 {
		c.Timeout = 30 * time.Second
	}
	mk := func(endpoint string) (*minio.Client, error) {
		u, err := url.Parse(endpoint)
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("media: bad S3 endpoint %q", endpoint)
		}
		cl, err := minio.New(u.Host, &minio.Options{
			Creds: credentials.NewStaticV4(c.AccessKey, c.SecretKey, ""), Secure: u.Scheme == "https",
			Region: c.Region, BucketLookup: minio.BucketLookupPath,
		})
		if err != nil {
			return nil, fmt.Errorf("media: S3 client: %w", err)
		}
		return cl, nil
	}
	client, err := mk(c.Endpoint)
	if err != nil {
		return nil, err
	}
	public, err := mk(c.PublicEndpoint)
	if err != nil {
		return nil, err
	}
	return &S3{cfg: c, client: client, public: public}, nil
}

// NewS3For returns the object store described by c, the media settings
// every role that stores objects reads from the environment.
func NewS3For(c config.Media) (*S3, error) {
	return NewS3(S3Config{
		Endpoint: c.S3Endpoint, PublicEndpoint: c.S3PublicEndpoint, Region: c.S3Region, Bucket: c.S3Bucket,
		AccessKey: c.S3AccessKey, SecretKey: c.S3SecretKey,
	})
}

// Bucket returns the bucket name.
func (s *S3) Bucket() string { return s.cfg.Bucket }

// PresignUpload returns a POST policy for exactly key and contentType,
// at most maxBytes, valid for ttl. The size limit is enforced by the
// storage service itself, before anything reaches our servers.
func (s *S3) PresignUpload(ctx context.Context, key, contentType string, maxBytes int64, ttl time.Duration) (Upload, error) {
	expires := time.Now().Add(ttl).UTC()
	p := minio.NewPostPolicy()
	if err := errors.Join(
		p.SetBucket(s.cfg.Bucket), p.SetKey(key), p.SetExpires(expires),
		p.SetContentType(contentType), p.SetContentLengthRange(1, maxBytes),
	); err != nil {
		return Upload{}, fmt.Errorf("media: post policy: %w", err)
	}
	u, fields, err := s.public.PresignedPostPolicy(ctx, p)
	if err != nil {
		return Upload{}, fmt.Errorf("media: presign: %w", err)
	}
	return Upload{URL: u.String(), Fields: fields, ExpiresAt: expires, MaxBytes: maxBytes}, nil
}

// Get returns the object at key, or ErrNoObject.
func (s *S3) Get(ctx context.Context, key string, maxBytes int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	obj, err := s.client.GetObject(ctx, s.cfg.Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, s3Err(err, "get")
	}
	defer func() { _ = obj.Close() }()
	data, err := io.ReadAll(io.LimitReader(obj, maxBytes+1))
	if err != nil {
		return nil, s3Err(err, "read")
	}
	return data, nil
}

// Put writes an immutable object.
func (s *S3) Put(ctx context.Context, key, contentType string, data []byte) error {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	_, err := s.client.PutObject(ctx, s.cfg.Bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType: contentType, CacheControl: "public, max-age=31536000, immutable",
	})
	if err != nil {
		return s3Err(err, "put")
	}
	return nil
}

// Delete removes key; a missing key is not an error (idempotent).
func (s *S3) Delete(ctx context.Context, key string) error {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	if err := s.client.RemoveObject(ctx, s.cfg.Bucket, key, minio.RemoveObjectOptions{}); err != nil {
		if errors.Is(s3Err(err, "delete"), ErrNoObject) {
			return nil
		}
		return s3Err(err, "delete")
	}
	return nil
}

func s3Err(err error, op string) error {
	if code := minio.ToErrorResponse(err).Code; code == "NoSuchKey" || code == "NoSuchObject" {
		return ErrNoObject
	}
	return fmt.Errorf("media: s3 %s: %w", op, err)
}
