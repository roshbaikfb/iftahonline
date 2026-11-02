package app_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/media"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// bucket is an in-memory media.Objects.
type bucket struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func (b *bucket) PresignUpload(_ context.Context, key, _ string, maxBytes int64, ttl time.Duration) (media.Upload, error) {
	return media.Upload{URL: "https://s3.test/media", Fields: map[string]string{"key": key}, ExpiresAt: time.Now().Add(ttl), MaxBytes: maxBytes}, nil
}

func (b *bucket) Get(_ context.Context, key string, _ int64) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	d, ok := b.objects[key]
	if !ok {
		return nil, media.ErrNoObject
	}
	return d, nil
}

func (b *bucket) Put(_ context.Context, key, _ string, data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.objects[key] = data
	return nil
}

func (b *bucket) Delete(_ context.Context, key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.objects, key)
	return nil
}

func (b *bucket) keys() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Sorted(maps.Keys(b.objects))
}

// upload puts a PNG where the console's browser would, and returns its
// key.
func (b *bucket) upload(t *testing.T, svc *app.Service) string {
	t.Helper()
	up, err := svc.UploadURL(t.Context(), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 320, 320))
	for x := range 320 {
		img.Set(x, x, color.RGBA{R: 15, G: 46, B: 82, A: 255})
	}
	var buf bytes.Buffer
	if err = png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err = b.Put(t.Context(), up.Key, "image/png", buf.Bytes()); err != nil {
		t.Fatal(err)
	}
	return up.Key
}

// TestAboutEndToEnd: the console writes the story and the video, the
// logo, founders with their photos and merchants' reviews; images are
// re-encoded under platform/site/, replaced ones and those of removed
// people are deleted; the site reads it all after its minute. One test:
// the content is the platform's, one set for the package's database.
func TestAboutEndToEnd(t *testing.T) {
	ctx := t.Context()
	appPool, adminPool := open(t, infra.AppURL), open(t, infra.AdminURL)
	clk := &clock{now: time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)}
	b := &bucket{objects: map[string][]byte{}}
	console := app.NewService(app.Deps{DB: adminPool, Admin: adminPool, IDs: ulid.NewGenerator(), Now: clk.Now,
		Media: app.Media{Objects: b, Limits: media.Limits{MaxBytes: 10 << 20, MaxPixels: 40_000_000}}})
	site := app.NewService(app.Deps{DB: appPool, Now: clk.Now})

	// Nothing yet: no people, no reviews, no story.
	c, err := site.Content(ctx)
	if err != nil || len(c.People) != 0 || len(c.Testimonials) != 0 || c.Identity.Story.Ar != "" || c.Identity.LogoKey != "" {
		t.Fatalf("empty content = %+v, %v", c.Identity, err)
	}
	if _, err = site.UploadURL(ctx, "image/png"); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("site pool uploads: %v", err)
	}
	if _, err = console.UploadURL(ctx, "image/svg+xml"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("an SVG upload: %v", err)
	}

	// The story and the video.
	if _, err = console.SaveStory(ctx, "قصة", "", "https://vimeo.com/1"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("a Vimeo link: %v", err)
	}
	st, err := console.SaveStory(ctx, " بدأنا من متجر.\n\nثم بنينا المنصة. ", "We started from a store.", "https://youtu.be/dQw4w9WgXcQ")
	if err != nil || st.VideoID != "dQw4w9WgXcQ" || !strings.HasPrefix(st.Ar, "بدأنا") {
		t.Fatalf("story = %+v, %v", st, err)
	}

	// The logo: re-encoded, replaced, removed.
	logo, err := console.SetImage(ctx, domain.Logo, b.upload(t, console))
	if err != nil || !strings.HasPrefix(logo, "platform/site/") {
		t.Fatalf("logo = %q, %v", logo, err)
	}
	if _, err = console.SetImage(ctx, domain.Logo, "platform/ads/uploads/x"); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("an upload outside the site's: %v", err)
	}
	if _, err = console.SetImage(ctx, "favicon", ""); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("an unknown image: %v", err)
	}
	logo2, err := console.SetImage(ctx, domain.Logo, b.upload(t, console))
	if err != nil {
		t.Fatal(err)
	}
	if keys := b.keys(); !slices.Contains(keys, logo2) || slices.Contains(keys, logo) || len(keys) != 1 {
		t.Fatalf("after replacing the logo, the bucket = %v", keys)
	}

	// A founder with a photo, a team member without.
	founder, err := console.CreatePerson(ctx, domain.Person{Kind: domain.Founder, NameAr: "أحمد", RoleAr: "المدير التقني", BioAr: "بنى المنصة.",
		LinkedIn: "https://www.linkedin.com/in/ahmed"}, b.upload(t, console))
	if err != nil || founder.PhotoKey == "" {
		t.Fatalf("founder = %+v, %v", founder, err)
	}
	member, err := console.CreatePerson(ctx, domain.Person{Kind: domain.Team, NameAr: "سارة", RoleAr: "الدعم"}, "")
	if err != nil || member.PhotoKey != "" {
		t.Fatalf("member = %+v, %v", member, err)
	}
	if _, err = console.CreatePerson(ctx, domain.Person{Kind: domain.Team, NameAr: "س", RoleAr: "الدعم"}, ""); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("a one-letter name: %v", err)
	}
	// An edit keeps the photo; a new upload replaces it; removing takes it off.
	founder.RoleEn = "CTO"
	if founder, err = console.UpdatePerson(ctx, founder, "", false); err != nil || founder.PhotoKey == "" {
		t.Fatalf("kept = %+v, %v", founder, err)
	}
	old := founder.PhotoKey
	if founder, err = console.UpdatePerson(ctx, founder, b.upload(t, console), false); err != nil || founder.PhotoKey == old || slices.Contains(b.keys(), old) {
		t.Fatalf("replaced = %+v (%v), %v", founder, b.keys(), err)
	}
	if _, err = console.UpdatePerson(ctx, domain.Person{ID: ulid.NewGenerator().New(), Kind: domain.Team, NameAr: "سارة", RoleAr: "الدعم"}, "", false); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("updating none: %v", err)
	}

	// A merchant's review.
	review, err := console.CreateTestimonial(ctx, domain.Testimonial{Name: "منى", StoreName: "متجر منى", QuoteAr: "قلّت الطلبات الوهمية عندي كثيرًا."}, "")
	if err != nil {
		t.Fatal(err)
	}

	// The site sees it all after its minute.
	if c, _ = site.Content(ctx); len(c.People) != 0 {
		t.Fatal("people seen before the minute")
	}
	clk.add(app.CacheTTL)
	c, err = site.Content(ctx)
	if err != nil || len(c.People) != 2 || c.People[0].Kind != domain.Founder || c.People[0].RoleEn != "CTO" || len(c.Testimonials) != 1 ||
		c.Identity.LogoKey != logo2 || c.Identity.Story.VideoID != "dQw4w9WgXcQ" {
		t.Fatalf("content = %+v, %v", c, err)
	}

	// Removing deletes the photos with them.
	if err = console.DeletePerson(ctx, founder.ID); err != nil {
		t.Fatal(err)
	}
	if err = console.DeletePerson(ctx, founder.ID); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("deleting twice: %v", err)
	}
	if err = console.DeleteTestimonial(ctx, review.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = console.SetImage(ctx, domain.Logo, ""); err != nil {
		t.Fatal(err)
	}
	if keys := b.keys(); len(keys) != 0 {
		t.Fatalf("left in the bucket: %v", keys)
	}
	if all, allErr := console.All(ctx); allErr != nil || len(all.People) != 1 || all.Identity.LogoKey != "" {
		t.Fatalf("all = %+v, %v", all, allErr)
	}
	if err = console.DeletePerson(ctx, member.ID); err != nil {
		t.Fatal(err)
	}
}

// TestImagesAndLimits: the share image and a review's photo are set,
// replaced and taken off like the logo; a file that is not an image is
// refused (and nothing saved); the console signs images' URLs only with
// a signer; uploads need the bucket; the people's limit holds.
func TestImagesAndLimits(t *testing.T) {
	ctx := t.Context()
	adminPool := open(t, infra.AdminURL)
	b := &bucket{objects: map[string][]byte{}}
	ids := ulid.NewGenerator()
	console := app.NewService(app.Deps{DB: adminPool, Admin: adminPool, IDs: ids,
		Media: app.Media{Objects: b, Limits: media.Limits{MaxBytes: 10 << 20, MaxPixels: 40_000_000},
			Signer: media.NewSigner("https://img.iftahonline.com", "media", []byte("key"), []byte("salt"))}})
	noBucket := app.NewService(app.Deps{DB: adminPool, Admin: adminPool, IDs: ids})

	if _, err := noBucket.UploadURL(ctx, "image/png"); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("an upload without a bucket: %v", err)
	}
	if _, err := noBucket.SetImage(ctx, domain.ShareImage, "platform/site/uploads/x"); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("an image without a bucket: %v", err)
	}
	if noBucket.ImageURL("platform/site/x.png", 320, "webp") != "" || console.ImageURL("", 320, "webp") != "" {
		t.Fatal("a URL without a signer or a key")
	}

	share, err := console.SetImage(ctx, domain.ShareImage, b.upload(t, console))
	if err != nil {
		t.Fatal(err)
	}
	if u := console.ImageURL(share, 1200, "jpg"); !strings.HasPrefix(u, "https://img.iftahonline.com/") || !strings.HasSuffix(u, ".jpg") {
		t.Fatalf("share URL = %q", u)
	}

	// Not an image: refused, nothing saved, the upload kept for a retry.
	up, err := console.UploadURL(ctx, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Put(ctx, up.Key, "image/png", []byte("<svg xmlns='http://www.w3.org/2000/svg'/>")); err != nil {
		t.Fatal(err)
	}
	if _, err = console.CreateTestimonial(ctx, domain.Testimonial{Name: "منى", QuoteAr: "قلّت الطلبات الوهمية عندي."}, up.Key); !errs.Is(err, errs.KindValidation) {
		t.Fatalf("an SVG passed as a PNG: %v", err)
	}
	if c, _ := console.All(ctx); len(c.Testimonials) != 0 {
		t.Fatalf("a refused review was saved: %+v", c.Testimonials)
	}

	// A review's photo: added, then taken off (and deleted).
	review, err := console.CreateTestimonial(ctx, domain.Testimonial{Name: "منى", QuoteAr: "قلّت الطلبات الوهمية عندي."}, b.upload(t, console))
	if err != nil || review.PhotoKey == "" {
		t.Fatalf("review = %+v, %v", review, err)
	}
	photo := review.PhotoKey
	if review, err = console.UpdateTestimonial(ctx, review, "", true); err != nil || review.PhotoKey != "" || slices.Contains(b.keys(), photo) {
		t.Fatalf("photo removed = %+v (%v), %v", review, b.keys(), err)
	}
	if _, err = console.UpdateTestimonial(ctx, domain.Testimonial{ID: ids.New(), Name: "منى", QuoteAr: "قلّت الطلبات الوهمية عندي."}, "", false); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("updating none: %v", err)
	}
	if err = console.DeleteTestimonial(ctx, review.ID); err != nil {
		t.Fatal(err)
	}
	if err = console.DeleteTestimonial(ctx, review.ID); !errs.Is(err, errs.KindNotFound) {
		t.Fatalf("deleting twice: %v", err)
	}
	if _, err = console.SetImage(ctx, domain.ShareImage, ""); err != nil {
		t.Fatal(err)
	}

	// The people's limit.
	var made []ulid.ID
	t.Cleanup(func() {
		for _, id := range made {
			if delErr := console.DeletePerson(context.Background(), id); delErr != nil {
				t.Error(delErr)
			}
		}
	})
	for range domain.MaxPeople {
		p, addErr := console.CreatePerson(ctx, domain.Person{Kind: domain.Team, NameAr: "عضو", RoleAr: "الدعم", Position: 1000}, "")
		if addErr != nil {
			t.Fatal(addErr)
		}
		made = append(made, p.ID)
	}
	if _, err = console.CreatePerson(ctx, domain.Person{Kind: domain.Team, NameAr: "زائد", RoleAr: "الدعم"}, ""); !errs.Is(err, errs.KindConflict) {
		t.Fatalf("one too many: %v", err)
	}
}
