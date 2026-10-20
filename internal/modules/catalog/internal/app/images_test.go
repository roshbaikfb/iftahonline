package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/media"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

var testLimits = media.Limits{MaxBytes: 1 << 20, MaxPixels: 1_000_000}

// memStore is an in-memory bucket. Uploads are simulated with put.
type memStore struct {
	mu       sync.Mutex
	objects  map[string][]byte
	presigns []string // "key content-type max"
}

func newMemStore() *memStore { return &memStore{objects: map[string][]byte{}} }

func (m *memStore) PresignUpload(_ context.Context, key, ct string, maxBytes int64, ttl time.Duration) (media.Upload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.presigns = append(m.presigns, key+" "+ct)
	return media.Upload{URL: "https://s3.test/bucket", Fields: map[string]string{"key": key, "Content-Type": ct},
		ExpiresAt: time.Now().Add(ttl), MaxBytes: maxBytes}, nil
}

func (m *memStore) Get(_ context.Context, key string, _ int64) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objects[key]
	if !ok {
		return nil, media.ErrNoObject
	}
	return b, nil
}

func (m *memStore) Put(_ context.Context, key, _ string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = data
	return nil
}

func (m *memStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

func (m *memStore) has(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.objects[key]
	return ok
}

func photo(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 0xC0
	}
	img.Set(0, 0, color.Black)
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// takeJobs returns, and deletes, the store's queued jobs of a kind.
func takeJobs[T river.JobArgs](ctx context.Context, t *testing.T, f fixture) []T {
	t.Helper()
	var zero T
	var out []T
	err := postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `DELETE FROM river_job WHERE kind = $1 AND args->>'store_id' = $2 RETURNING args, queue`,
			zero.Kind(), mustStore(ctx, t).String())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			var queue string
			if err := rows.Scan(&raw, &queue); err != nil {
				return err
			}
			if zero.Kind() == "catalog.process_image" && queue != app.QueueMedia {
				t.Errorf("process job on queue %q, want %q", queue, app.QueueMedia)
			}
			var a T
			if err := json.Unmarshal(raw, &a); err != nil {
				return err
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func workers(f fixture) *app.ImageWorkers {
	return &app.ImageWorkers{DB: f.pool, Jobs: f.ins, Media: app.Media{Objects: f.objects, Limits: testLimits},
		Events: app.Events{Pub: f.events, IDs: f.ids}}
}

// runJobs runs every queued image job of the store until none is left,
// as the worker would.
func runJobs(ctx context.Context, t *testing.T, f fixture) {
	t.Helper()
	w := workers(f)
	for range 5 {
		ran := 0
		for _, a := range takeJobs[app.ProcessImageArgs](ctx, t, f) {
			if err := (&app.ProcessWorker{ImageWorkers: w}).Work(ctx, &river.Job[app.ProcessImageArgs]{Args: a}); err != nil {
				t.Fatalf("process: %v", err)
			}
			ran++
		}
		for _, a := range takeJobs[app.DeleteObjectsArgs](ctx, t, f) {
			if err := (&app.DeleteObjectsWorker{ImageWorkers: w}).Work(ctx, &river.Job[app.DeleteObjectsArgs]{Args: a}); err != nil {
				t.Fatalf("delete objects: %v", err)
			}
			ran++
		}
		if ran == 0 {
			return
		}
	}
}

func TestImagePipeline(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.ctxA
	p := mustCreate(ctx, t, f, single("With photos", 1, true))

	img, up, err := f.svc.CreateImageUpload(ctx, p.ID, " IMAGE/JPEG ", domain.ImageInput{Alt: " صورة أمامية ", Position: 1})
	if err != nil {
		t.Fatal(err)
	}
	store := mustStore(ctx, t)
	if img.Status != domain.ImagePending || img.Alt != "صورة أمامية" || img.UploadKey != domain.UploadKey(store, img.ID) {
		t.Fatalf("image %+v", img)
	}
	if up.MaxBytes != testLimits.MaxBytes || up.Fields["Content-Type"] != "image/jpeg" || time.Until(up.ExpiresAt) > domain.UploadURLTTL {
		t.Fatalf("upload %+v", up)
	}
	if expire := takeJobs[app.ExpireUploadArgs](ctx, t, f); len(expire) != 1 || expire[0].ImageID != img.ID {
		t.Fatalf("expiry jobs = %+v", expire)
	}

	// The browser uploads; then the dashboard completes.
	if err = f.objects.Put(ctx, img.UploadKey, "image/jpeg", photo(t, 60, 30)); err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.CompleteImageUpload(ctx, p.ID, img.ID)
	if err != nil || got.Status != domain.ImageProcessing {
		t.Fatalf("complete: %v %+v", err, got)
	}
	if again, againErr := f.svc.CompleteImageUpload(ctx, p.ID, img.ID); againErr != nil || again.Status != domain.ImageProcessing {
		t.Fatalf("second complete: %v %+v", againErr, again)
	}
	runJobs(ctx, t, f)

	prod, err := f.svc.GetProduct(ctx, p.ID)
	if err != nil || len(prod.Images) != 1 {
		t.Fatalf("product images: %v %+v", err, prod.Images)
	}
	ready := prod.Images[0]
	if ready.Status != domain.ImageReady || ready.Width != 60 || ready.Height != 30 || ready.Format != "jpeg" ||
		ready.ObjectKey != domain.MasterKey(store, img.ID, "jpeg") || ready.Bytes == 0 {
		t.Fatalf("ready image %+v", ready)
	}
	if !f.objects.has(ready.ObjectKey) || f.objects.has(img.UploadKey) {
		t.Fatal("master not stored, or original not removed")
	}

	upd, err := f.svc.UpdateImage(ctx, p.ID, img.ID, domain.ImageInput{Alt: "front", Position: 0})
	if err != nil || upd.Alt != "front" || upd.Position != 0 {
		t.Fatalf("update: %v %+v", err, upd)
	}
	if err = f.svc.DeleteImage(ctx, p.ID, img.ID); err != nil {
		t.Fatal(err)
	}
	runJobs(ctx, t, f)
	if f.objects.has(ready.ObjectKey) {
		t.Fatal("master survived deletion")
	}
	wantKind(t, f.svc.DeleteImage(ctx, p.ID, img.ID), errs.KindNotFound)
	for _, a := range []string{"image.created", "image.updated", "image.deleted"} {
		if !f.audit.has(a) {
			t.Errorf("%s not audited", a)
		}
	}
}

func TestImageRejections(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.ctxA
	p := mustCreate(ctx, t, f, single("Rejects", 1, true))
	cases := []struct {
		name   string
		data   []byte
		reason string
	}{
		{"svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`), "unsupported_type"},
		{"html renamed .jpg", []byte(`<!doctype html><script>x</script>`), "unsupported_type"},
		{"too big", bytes.Repeat([]byte{0xFF}, int(testLimits.MaxBytes)+10), "file_too_large"},
		{"too many pixels", photo(t, 1100, 1000), "too_many_pixels"},
		{"never uploaded", nil, "upload_missing"},
	}
	for _, c := range cases {
		img, _, err := f.svc.CreateImageUpload(ctx, p.ID, "image/jpeg", domain.ImageInput{})
		if err != nil {
			t.Fatal(err)
		}
		if c.data != nil {
			_ = f.objects.Put(ctx, img.UploadKey, "image/jpeg", c.data)
		}
		if _, err = f.svc.CompleteImageUpload(ctx, p.ID, img.ID); err != nil {
			t.Fatal(err)
		}
		runJobs(ctx, t, f)
		got, err := f.svc.GetProduct(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		var found domain.Image
		for _, im := range got.Images {
			if im.ID == img.ID {
				found = im
			}
		}
		if found.Status != domain.ImageFailed || found.FailureReason != c.reason || f.objects.has(img.UploadKey) {
			t.Errorf("%s: %+v (original kept: %v)", c.name, found, f.objects.has(img.UploadKey))
		}
		if _, err = f.svc.CompleteImageUpload(ctx, p.ID, img.ID); !errs.Is(err, errs.KindConflict) {
			t.Errorf("%s: completing a failed image: %v", c.name, err)
		}
	}
}

func TestImageLimitsAndValidation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.ctxA
	p := mustCreate(ctx, t, f, single("Many photos", 1, true))
	for range domain.MaxProductImages {
		if _, _, err := f.svc.CreateImageUpload(ctx, p.ID, "image/png", domain.ImageInput{}); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := f.svc.CreateImageUpload(ctx, p.ID, "image/png", domain.ImageInput{})
	wantKind(t, err, errs.KindConflict)

	_, _, err = f.svc.CreateImageUpload(ctx, p.ID, "image/svg+xml", domain.ImageInput{Alt: strings.Repeat("a", 201), Position: -1})
	wantField(t, err, "content_type", "unsupported")
	wantField(t, err, "alt", "length")
	wantField(t, err, "position", "range")
	_, _, err = f.svc.CreateImageUpload(ctx, f.ids.New(), "image/png", domain.ImageInput{})
	wantKind(t, err, errs.KindNotFound)
	_, err = f.svc.UpdateImage(ctx, p.ID, f.ids.New(), domain.ImageInput{Position: 999})
	wantField(t, err, "position", "range")
	_, err = f.svc.CompleteImageUpload(ctx, p.ID, f.ids.New())
	wantKind(t, err, errs.KindNotFound)
}

func TestImageExpiryAndProductDeletion(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.ctxA
	p := mustCreate(ctx, t, f, single("Doomed", 1, true))
	w := workers(f)
	expire := func(id ulid.ID) error {
		args := app.ExpireUploadArgs{TenantArgs: jobs.TenantArgs{StoreID: mustStore(ctx, t)}, ImageID: id}
		return (&app.ExpireUploadWorker{ImageWorkers: w}).Work(ctx, &river.Job[app.ExpireUploadArgs]{Args: args})
	}

	abandoned, _, err := f.svc.CreateImageUpload(ctx, p.ID, "image/jpeg", domain.ImageInput{})
	if err != nil {
		t.Fatal(err)
	}
	_ = f.objects.Put(ctx, abandoned.UploadKey, "image/jpeg", []byte("partial"))
	if err = expire(abandoned.ID); err != nil {
		t.Fatal(err)
	}
	runJobs(ctx, t, f)
	if got, _ := f.svc.GetProduct(ctx, p.ID); len(got.Images) != 0 || f.objects.has(abandoned.UploadKey) {
		t.Fatalf("abandoned upload kept: %+v", got.Images)
	}

	// A completed image survives its expiry job.
	kept, _, _ := f.svc.CreateImageUpload(ctx, p.ID, "image/jpeg", domain.ImageInput{})
	_ = f.objects.Put(ctx, kept.UploadKey, "image/jpeg", photo(t, 10, 10))
	if _, err = f.svc.CompleteImageUpload(ctx, p.ID, kept.ID); err != nil {
		t.Fatal(err)
	}
	runJobs(ctx, t, f)
	if err = expire(kept.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := f.svc.GetProduct(ctx, p.ID)
	if len(got.Images) != 1 || got.Images[0].Status != domain.ImageReady {
		t.Fatalf("completed image expired: %+v", got.Images)
	}
	master := got.Images[0].ObjectKey

	// Deleting the product removes its images and files.
	if err = f.svc.DeleteProduct(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	runJobs(ctx, t, f)
	if f.objects.has(master) {
		t.Fatal("product deletion left the master behind")
	}
}

func TestDeleteObjectsRefusesForeignKeys(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	other := domain.StorePrefix(mustStore(f.ctxB, t)) + "images/x.jpg"
	_ = f.objects.Put(f.ctxB, other, "image/jpeg", []byte("b"))
	for _, key := range []string{other, domain.StorePrefix(mustStore(f.ctxA, t)) + "../" + other, "elsewhere/x"} {
		args := app.DeleteObjectsArgs{TenantArgs: jobs.TenantArgs{StoreID: mustStore(f.ctxA, t)}, Keys: []string{key}}
		err := (&app.DeleteObjectsWorker{ImageWorkers: workers(f)}).Work(f.ctxA, &river.Job[app.DeleteObjectsArgs]{Args: args})
		var cancel *river.JobCancelError
		if !errors.As(err, &cancel) {
			t.Fatalf("%s: err = %v, want a cancelled job", key, err)
		}
	}
	if !f.objects.has(other) {
		t.Fatal("store A's job deleted store B's object")
	}
}

func TestImagesCrossTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	pb := mustCreate(f.ctxB, t, f, single("B product", 1, true))
	imgB, _, err := f.svc.CreateImageUpload(f.ctxB, pb.ID, "image/jpeg", domain.ImageInput{Alt: "B"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = f.svc.CreateImageUpload(f.ctxA, pb.ID, "image/jpeg", domain.ImageInput{})
	wantKind(t, err, errs.KindNotFound)
	_, err = f.svc.CompleteImageUpload(f.ctxA, pb.ID, imgB.ID)
	wantKind(t, err, errs.KindNotFound)
	_, err = f.svc.UpdateImage(f.ctxA, pb.ID, imgB.ID, domain.ImageInput{Alt: "pwned"})
	wantKind(t, err, errs.KindNotFound)
	wantKind(t, f.svc.DeleteImage(f.ctxA, pb.ID, imgB.ID), errs.KindNotFound)

	// A's worker cannot process B's image either: RLS hides the row.
	args := app.ProcessImageArgs{TenantArgs: jobs.TenantArgs{StoreID: mustStore(f.ctxA, t)}, ImageID: imgB.ID}
	if err = (&app.ProcessWorker{ImageWorkers: workers(f)}).Work(f.ctxA, &river.Job[app.ProcessImageArgs]{Args: args}); err != nil {
		t.Fatal(err)
	}
	got, _ := f.svc.GetProduct(f.ctxB, pb.ID)
	if len(got.Images) != 1 || got.Images[0].Alt != "B" || got.Images[0].Status != domain.ImagePending {
		t.Fatalf("B's image changed: %+v", got.Images)
	}
}
