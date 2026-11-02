package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/media"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// The about page's content and the site's images (S4b-1), as the console
// edits them.

var (
	errNoMedia        = errs.Unavailable(0, errors.New("site: the bucket is not wired"))
	errPersonNotFound = errs.NotFound("person_not_found", "person not found")
	errReviewNotFound = errs.NotFound("testimonial_not_found", "review not found")
	errTooManyPeople  = errs.Conflict("too_many_people", "the about page holds at most 50 people")
	errTooManyReviews = errs.Conflict("too_many_testimonials", "the site holds at most 50 reviews")
	errUploadKey      = errs.Validation(errs.FieldError{Field: "upload_key", Code: "invalid", Message: "upload the image first"})
	errUnknownImage   = errs.NotFound("image_not_found", "the site has no such image")
)

// uploadPrefix is where the console's browser posts images before they
// are re-encoded.
const uploadPrefix = "platform/site/uploads/"

// UploadURLTTL is how long an image's upload URL works.
const UploadURLTTL = 10 * time.Minute

// Upload is where the console's browser posts an image (an S3 POST
// policy, as the ads' banners).
type Upload struct {
	Key       string
	URL       string
	Fields    map[string]string
	ExpiresAt time.Time
	MaxBytes  int64
}

// ImageURL is an image's signed URL fitted into size × size, in format
// (webp, avif or jpg); "" without a key or a signer.
func (s *Service) ImageURL(key string, size int, format string) string {
	if key == "" || s.d.Media.Signer == nil {
		return ""
	}
	return s.d.Media.Signer.URL(key, size, format)
}

// UploadURL is a presigned upload of an image (JPEG, PNG or WebP: never
// SVG).
func (s *Service) UploadURL(ctx context.Context, contentType string) (Upload, error) {
	if _, err := s.admin(); err != nil {
		return Upload{}, err
	}
	if s.d.Media.Objects == nil {
		return Upload{}, errNoMedia
	}
	switch contentType {
	case "image/jpeg", "image/png", "image/webp":
	default:
		return Upload{}, errs.Validation(errs.FieldError{Field: "content_type", Code: "invalid", Message: "JPEG, PNG or WebP (never SVG)"})
	}
	key := uploadPrefix + s.d.IDs.New().String()
	up, err := s.d.Media.Objects.PresignUpload(ctx, key, contentType, domain.MaxImageBytes, UploadURLTTL)
	if err != nil {
		return Upload{}, fmt.Errorf("site: presign: %w", err)
	}
	return Upload{Key: key, URL: up.URL, Fields: up.Fields, ExpiresAt: up.ExpiresAt, MaxBytes: domain.MaxImageBytes}, nil
}

// ingest re-encodes an upload (its real type detected, no metadata:
// CLAUDE.md upload rules) into a new master, and returns its key.
func (s *Service) ingest(ctx context.Context, uploadKey string) (string, error) {
	if s.d.Media.Objects == nil {
		return "", errNoMedia
	}
	if !strings.HasPrefix(uploadKey, uploadPrefix) || len(uploadKey) == len(uploadPrefix) {
		return "", errUploadKey
	}
	id := s.d.IDs.New()
	lim := s.d.Media.Limits
	lim.MaxBytes = min(lim.MaxBytes, domain.MaxImageBytes)
	_, key, err := media.Ingest(ctx, s.d.Media.Objects, lim, uploadKey, func(format string) string { return domain.ImageKey(id, format) })
	if code, rejected := media.IsReject(err); rejected {
		return "", errs.Validation(errs.FieldError{Field: "upload_key", Code: code, Message: "the image was rejected"})
	}
	if err != nil {
		return "", err
	}
	s.drop(ctx, uploadKey)
	return key, nil
}

// drop deletes an object no longer used: a failure leaves only a stray
// object, so it is logged, not returned.
func (s *Service) drop(ctx context.Context, key string) {
	if key == "" || s.d.Media.Objects == nil {
		return
	}
	if err := s.d.Media.Objects.Delete(ctx, key); err != nil {
		s.d.Log.WarnContext(ctx, "site: delete an image", "key", key, "err", err)
	}
}

// SaveStory writes the founding story and the video (a YouTube link or
// "").
func (s *Service) SaveStory(ctx context.Context, ar, en, videoLink string) (domain.Story, error) {
	db, err := s.admin()
	if err != nil {
		return domain.Story{}, err
	}
	id, err := domain.ParseVideo(videoLink)
	if err != nil {
		return domain.Story{}, err
	}
	st := domain.Story{Ar: ar, En: en, VideoID: id}
	st.Normalize()
	if err = st.Validate(); err != nil {
		return domain.Story{}, err
	}
	return st, store.SaveStory(ctx, db, st)
}

// SetImage makes an upload the site's logo or share image; uploadKey ""
// removes it. The old image is deleted.
func (s *Service) SetImage(ctx context.Context, which domain.Image, uploadKey string) (string, error) {
	db, err := s.admin()
	if err != nil {
		return "", err
	}
	if which != domain.Logo && which != domain.ShareImage {
		return "", errUnknownImage
	}
	_, before, err := store.SettingsAndIdentity(ctx, db)
	if err != nil {
		return "", err
	}
	key := ""
	if uploadKey != "" {
		if key, err = s.ingest(ctx, uploadKey); err != nil {
			return "", err
		}
	}
	if err = store.SetImage(ctx, db, which, key); err != nil {
		s.drop(ctx, key)
		return "", err
	}
	if which == domain.Logo {
		s.drop(ctx, before.LogoKey)
	} else {
		s.drop(ctx, before.ShareKey)
	}
	return key, nil
}

// photo is a person's or a review's photo after an edit: a new upload,
// none (remove), or the one it had.
func (s *Service) photo(ctx context.Context, had, uploadKey string, remove bool) (string, error) {
	switch {
	case uploadKey != "":
		return s.ingest(ctx, uploadKey)
	case remove:
		return "", nil
	default:
		return had, nil
	}
}

// entry is a person or a review: what it checks, and where its ID and
// photo are.
type entry[T any] interface {
	*T
	Check() error
	Keys() (id *ulid.ID, photo *string)
}

// items are the about page's lists the console edits one entry at a
// time: the people and the reviews. Both keep a photo, ingested on save
// and deleted with the entry or when replaced.
type items[T any] struct {
	max               int
	tooMany, notFound error
	list              func(context.Context, store.Tx) ([]T, error)
	get               func(context.Context, store.Tx, ulid.ID) (T, bool, error)
	insert            func(context.Context, store.Tx, T) error
	update            func(context.Context, store.Tx, T) (bool, error)
	remove            func(context.Context, store.Tx, ulid.ID) (bool, error)
}

func peopleItems() items[domain.Person] {
	return items[domain.Person]{domain.MaxPeople, errTooManyPeople, errPersonNotFound,
		store.People, store.Person, store.InsertPerson, store.UpdatePerson, store.DeletePerson}
}

func reviewItems() items[domain.Testimonial] {
	return items[domain.Testimonial]{domain.MaxTestimonials, errTooManyReviews, errReviewNotFound,
		store.Testimonials, store.Testimonial, store.InsertTestimonial, store.UpdateTestimonial, store.DeleteTestimonial}
}

// create adds an entry with a new ID, with an uploaded photo or none.
func create[T any, P entry[T]](ctx context.Context, s *Service, it items[T], v T, uploadKey string) (T, error) {
	var zero T
	db, err := s.admin()
	if err != nil {
		return zero, err
	}
	if err = P(&v).Check(); err != nil {
		return zero, err
	}
	all, err := it.list(ctx, db)
	if err != nil {
		return zero, err
	}
	if len(all) >= it.max {
		return zero, it.tooMany
	}
	id, photo := P(&v).Keys()
	if *photo, err = s.photo(ctx, "", uploadKey, false); err != nil {
		return zero, err
	}
	*id = s.d.IDs.New()
	if err = it.insert(ctx, db, v); err != nil {
		s.drop(ctx, *photo)
		return zero, err
	}
	return v, nil
}

// edit rewrites an entry: a new photo replaces the old one (deleted),
// removePhoto takes it off, neither keeps it.
func edit[T any, P entry[T]](ctx context.Context, s *Service, it items[T], v T, uploadKey string, removePhoto bool) (T, error) {
	var zero T
	db, err := s.admin()
	if err != nil {
		return zero, err
	}
	if err = P(&v).Check(); err != nil {
		return zero, err
	}
	id, photo := P(&v).Keys()
	had, found, err := it.get(ctx, db, *id)
	if err != nil {
		return zero, err
	}
	if !found {
		return zero, it.notFound
	}
	_, old := P(&had).Keys()
	if *photo, err = s.photo(ctx, *old, uploadKey, removePhoto); err != nil {
		return zero, err
	}
	if _, err = it.update(ctx, db, v); err != nil {
		if *photo != *old {
			s.drop(ctx, *photo)
		}
		return zero, err
	}
	if *photo != *old {
		s.drop(ctx, *old)
	}
	return v, nil
}

// remove deletes an entry and its photo.
func remove[T any, P entry[T]](ctx context.Context, s *Service, it items[T], id ulid.ID) error {
	db, err := s.admin()
	if err != nil {
		return err
	}
	had, found, err := it.get(ctx, db, id)
	if err != nil {
		return err
	}
	if !found {
		return it.notFound
	}
	if found, err = it.remove(ctx, db, id); err != nil {
		return err
	}
	if !found {
		return it.notFound
	}
	_, photo := P(&had).Keys()
	s.drop(ctx, *photo)
	return nil
}

// CreatePerson adds a founder or a team member, with an uploaded photo
// or none.
func (s *Service) CreatePerson(ctx context.Context, p domain.Person, uploadKey string) (domain.Person, error) {
	return create[domain.Person, *domain.Person](ctx, s, peopleItems(), p, uploadKey)
}

// UpdatePerson rewrites a person: a new photo replaces the old one,
// removePhoto takes it off, neither keeps it.
func (s *Service) UpdatePerson(ctx context.Context, p domain.Person, uploadKey string, removePhoto bool) (domain.Person, error) {
	return edit[domain.Person, *domain.Person](ctx, s, peopleItems(), p, uploadKey, removePhoto)
}

// DeletePerson removes a person and their photo.
func (s *Service) DeletePerson(ctx context.Context, id ulid.ID) error {
	return remove[domain.Person, *domain.Person](ctx, s, peopleItems(), id)
}

// CreateTestimonial adds a merchant's review.
func (s *Service) CreateTestimonial(ctx context.Context, t domain.Testimonial, uploadKey string) (domain.Testimonial, error) {
	return create[domain.Testimonial, *domain.Testimonial](ctx, s, reviewItems(), t, uploadKey)
}

// UpdateTestimonial rewrites a review, its photo as UpdatePerson.
func (s *Service) UpdateTestimonial(ctx context.Context, t domain.Testimonial, uploadKey string, removePhoto bool) (domain.Testimonial, error) {
	return edit[domain.Testimonial, *domain.Testimonial](ctx, s, reviewItems(), t, uploadKey, removePhoto)
}

// DeleteTestimonial removes a review and its photo.
func (s *Service) DeleteTestimonial(ctx context.Context, id ulid.ID) error {
	return remove[domain.Testimonial, *domain.Testimonial](ctx, s, reviewItems(), id)
}
