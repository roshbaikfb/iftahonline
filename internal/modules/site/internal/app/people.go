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

// CreatePerson adds a founder or a team member, with an uploaded photo
// or none.
func (s *Service) CreatePerson(ctx context.Context, p domain.Person, uploadKey string) (domain.Person, error) {
	db, err := s.admin()
	if err != nil {
		return domain.Person{}, err
	}
	p.Normalize()
	if err = p.Validate(); err != nil {
		return domain.Person{}, err
	}
	all, err := store.People(ctx, db)
	if err != nil {
		return domain.Person{}, err
	}
	if len(all) >= domain.MaxPeople {
		return domain.Person{}, errTooManyPeople
	}
	if p.PhotoKey, err = s.photo(ctx, "", uploadKey, false); err != nil {
		return domain.Person{}, err
	}
	p.ID = s.d.IDs.New()
	if err = store.InsertPerson(ctx, db, p); err != nil {
		s.drop(ctx, p.PhotoKey)
		return domain.Person{}, err
	}
	return p, nil
}

// UpdatePerson rewrites a person: a new photo replaces the old one,
// removePhoto takes it off, neither keeps it.
func (s *Service) UpdatePerson(ctx context.Context, p domain.Person, uploadKey string, removePhoto bool) (domain.Person, error) {
	db, err := s.admin()
	if err != nil {
		return domain.Person{}, err
	}
	p.Normalize()
	if err = p.Validate(); err != nil {
		return domain.Person{}, err
	}
	had, found, err := store.Person(ctx, db, p.ID)
	if err != nil {
		return domain.Person{}, err
	}
	if !found {
		return domain.Person{}, errPersonNotFound
	}
	if p.PhotoKey, err = s.photo(ctx, had.PhotoKey, uploadKey, removePhoto); err != nil {
		return domain.Person{}, err
	}
	if _, err = store.UpdatePerson(ctx, db, p); err != nil {
		if p.PhotoKey != had.PhotoKey {
			s.drop(ctx, p.PhotoKey)
		}
		return domain.Person{}, err
	}
	if p.PhotoKey != had.PhotoKey {
		s.drop(ctx, had.PhotoKey)
	}
	return p, nil
}

// DeletePerson removes a person and their photo.
func (s *Service) DeletePerson(ctx context.Context, id ulid.ID) error {
	db, err := s.admin()
	if err != nil {
		return err
	}
	had, found, err := store.Person(ctx, db, id)
	if err != nil {
		return err
	}
	if !found {
		return errPersonNotFound
	}
	if found, err = store.DeletePerson(ctx, db, id); err != nil {
		return err
	}
	if !found {
		return errPersonNotFound
	}
	s.drop(ctx, had.PhotoKey)
	return nil
}

// CreateTestimonial adds a merchant's review.
func (s *Service) CreateTestimonial(ctx context.Context, t domain.Testimonial, uploadKey string) (domain.Testimonial, error) {
	db, err := s.admin()
	if err != nil {
		return domain.Testimonial{}, err
	}
	t.Normalize()
	if err = t.Validate(); err != nil {
		return domain.Testimonial{}, err
	}
	all, err := store.Testimonials(ctx, db)
	if err != nil {
		return domain.Testimonial{}, err
	}
	if len(all) >= domain.MaxTestimonials {
		return domain.Testimonial{}, errTooManyReviews
	}
	if t.PhotoKey, err = s.photo(ctx, "", uploadKey, false); err != nil {
		return domain.Testimonial{}, err
	}
	t.ID = s.d.IDs.New()
	if err = store.InsertTestimonial(ctx, db, t); err != nil {
		s.drop(ctx, t.PhotoKey)
		return domain.Testimonial{}, err
	}
	return t, nil
}

// UpdateTestimonial rewrites a review, its photo as UpdatePerson.
func (s *Service) UpdateTestimonial(ctx context.Context, t domain.Testimonial, uploadKey string, removePhoto bool) (domain.Testimonial, error) {
	db, err := s.admin()
	if err != nil {
		return domain.Testimonial{}, err
	}
	t.Normalize()
	if err = t.Validate(); err != nil {
		return domain.Testimonial{}, err
	}
	had, found, err := store.Testimonial(ctx, db, t.ID)
	if err != nil {
		return domain.Testimonial{}, err
	}
	if !found {
		return domain.Testimonial{}, errReviewNotFound
	}
	if t.PhotoKey, err = s.photo(ctx, had.PhotoKey, uploadKey, removePhoto); err != nil {
		return domain.Testimonial{}, err
	}
	if _, err = store.UpdateTestimonial(ctx, db, t); err != nil {
		if t.PhotoKey != had.PhotoKey {
			s.drop(ctx, t.PhotoKey)
		}
		return domain.Testimonial{}, err
	}
	if t.PhotoKey != had.PhotoKey {
		s.drop(ctx, had.PhotoKey)
	}
	return t, nil
}

// DeleteTestimonial removes a review and its photo.
func (s *Service) DeleteTestimonial(ctx context.Context, id ulid.ID) error {
	db, err := s.admin()
	if err != nil {
		return err
	}
	had, found, err := store.Testimonial(ctx, db, id)
	if err != nil {
		return err
	}
	if !found {
		return errReviewNotFound
	}
	if found, err = store.DeleteTestimonial(ctx, db, id); err != nil {
		return err
	}
	if !found {
		return errReviewNotFound
	}
	s.drop(ctx, had.PhotoKey)
	return nil
}
