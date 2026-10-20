package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/storefront/internal/sections"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/media"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// memObjects is an in-memory bucket; uploads are simulated with Put.
type memObjects struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func (m *memObjects) PresignUpload(_ context.Context, key, ct string, maxBytes int64, ttl time.Duration) (media.Upload, error) {
	return media.Upload{URL: "https://s3.test/bucket", Fields: map[string]string{"key": key, "Content-Type": ct},
		MaxBytes: maxBytes, ExpiresAt: time.Now().Add(ttl)}, nil
}

func (m *memObjects) Get(_ context.Context, key string, _ int64) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objects[key]
	if !ok {
		return nil, media.ErrNoObject
	}
	return b, nil
}

func (m *memObjects) Put(_ context.Context, key, _ string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = data
	return nil
}

func (m *memObjects) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

func (m *memObjects) has(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.objects[key]
	return ok
}

func logoPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 0x80 // half-transparent grey: stays PNG
	}
	img.Set(0, 0, color.NRGBA{R: 255, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

type assetFixture struct {
	fixture
	pool    *pgxpool.Pool
	objects *memObjects
	workers *app.AssetWorkers
}

func newAssetFixture(t *testing.T) assetFixture {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), infra.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ins, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Middleware: jobs.Middleware()})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := sections.New()
	if err != nil {
		t.Fatal(err)
	}
	ids := ulid.NewGenerator()
	l := &log{}
	objs := &memObjects{objects: map[string][]byte{}}
	m := app.Media{Objects: objs, Limits: media.Limits{MaxBytes: 8 << 20, MaxPixels: 4 << 20}}
	f := assetFixture{
		fixture: fixture{svc: app.NewService(app.Deps{DB: pool, IDs: ids, Audit: l, Events: l, Sections: reg, Jobs: ins, Media: m}), log: l},
		pool:    pool, objects: objs,
		workers: &app.AssetWorkers{DB: pool, Jobs: ins, Media: m, Events: l, IDs: ids},
	}
	f.ctxA = createStore(t, pool, ids)
	f.ctxB = createStore(t, pool, ids)
	return f
}

// takeJobs removes and returns the store's queued jobs of T's kind.
func takeJobs[T river.JobArgs](ctx context.Context, t *testing.T, f assetFixture) []T {
	t.Helper()
	var zero T
	store, err := tenant.FromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var out []T
	err = postgres.InTenantTx(ctx, f.pool, func(tx pgx.Tx) error {
		rows, qErr := tx.Query(ctx, `DELETE FROM river_job WHERE kind = $1 AND args->>'store_id' = $2 RETURNING args`, zero.Kind(), store.String())
		if qErr != nil {
			return qErr
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			if scanErr := rows.Scan(&raw); scanErr != nil {
				return scanErr
			}
			var a T
			if jsonErr := json.Unmarshal(raw, &a); jsonErr != nil {
				return jsonErr
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

func (f assetFixture) process(ctx context.Context, t *testing.T) {
	t.Helper()
	for _, a := range takeJobs[app.ProcessAssetArgs](ctx, t, f) {
		if err := (&app.ProcessAssetWorker{AssetWorkers: f.workers}).Work(ctx, &river.Job[app.ProcessAssetArgs]{Args: a}); err != nil {
			t.Fatalf("process: %v", err)
		}
	}
}

// deleted runs the queued delete jobs and returns the keys they removed.
func (f assetFixture) deleted(ctx context.Context, t *testing.T) []string {
	t.Helper()
	var keys []string
	for _, a := range takeJobs[app.DeleteObjectsArgs](ctx, t, f) {
		if err := (&app.DeleteObjectsWorker{AssetWorkers: f.workers}).Work(ctx, &river.Job[app.DeleteObjectsArgs]{Args: a}); err != nil {
			t.Fatalf("delete objects: %v", err)
		}
		keys = append(keys, a.Keys...)
	}
	slices.Sort(keys)
	return keys
}

func (f assetFixture) upload(ctx context.Context, t *testing.T, data []byte) domain.Asset {
	t.Helper()
	a, up, err := f.svc.CreateLogoUpload(ctx, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.objects.Put(ctx, up.Fields["key"], "image/png", data); err != nil {
		t.Fatal(err)
	}
	if a, err = f.svc.CompleteLogoUpload(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestLogoLifecycle(t *testing.T) {
	t.Parallel()
	f := newAssetFixture(t)
	ctx := f.ctxA
	storeA, err := tenant.FromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if l, getErr := f.svc.GetLogo(ctx); getErr != nil || l.Current != nil || l.Latest != nil {
		t.Fatalf("no logo yet: %+v %v", l, getErr)
	}
	_, _, err = f.svc.CreateLogoUpload(ctx, "image/svg+xml")
	wantFields(t, err, "content_type:unsupported")

	first, up, err := f.svc.CreateLogoUpload(ctx, " IMAGE/PNG ")
	if err != nil || first.Status != domain.AssetPending || first.Kind != domain.AssetLogo {
		t.Fatalf("create: %+v %v", first, err)
	}
	if up.Fields["key"] != domain.AssetUploadKey(storeA, first.ID) || up.MaxBytes != domain.MaxLogoBytes || up.Fields["Content-Type"] != "image/png" {
		t.Fatalf("upload %+v", up)
	}
	if exp := takeJobs[app.ExpireAssetUploadArgs](ctx, t, f); len(exp) != 1 || exp[0].AssetID != first.ID {
		t.Fatalf("expiry jobs %+v", exp)
	}
	if err = f.objects.Put(ctx, first.UploadKey, "image/png", logoPNG(t, 300, 100)); err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.CompleteLogoUpload(ctx, first.ID)
	if err != nil || got.Status != domain.AssetProcessing {
		t.Fatalf("complete: %+v %v", got, err)
	}
	if again, againErr := f.svc.CompleteLogoUpload(ctx, first.ID); againErr != nil || again.Status != domain.AssetProcessing {
		t.Fatalf("complete twice: %+v %v", again, againErr)
	}
	f.log.take()
	f.process(ctx, t)
	l, err := f.svc.GetLogo(ctx)
	if err != nil || l.Current == nil || l.Latest != nil {
		t.Fatalf("after processing: %+v %v", l, err)
	}
	cur := *l.Current
	if cur.ID != first.ID || cur.Status != domain.AssetReady || cur.Width != 300 || cur.Height != 100 || cur.Format != "png" ||
		cur.ObjectKey != domain.AssetMasterKey(storeA, first.ID, "png") || !f.objects.has(cur.ObjectKey) {
		t.Fatalf("ready logo %+v", cur)
	}
	if ev, _ := f.log.take(); !slices.Equal(ev, []string{domain.EventThemeChanged}) {
		t.Fatalf("events %q", ev)
	}
	if keys := f.deleted(ctx, t); !slices.Equal(keys, []string{first.UploadKey}) || f.objects.has(first.UploadKey) {
		t.Fatalf("deleted %q", keys)
	}

	// A replacement goes live only when ready, and removes the old master.
	second := f.upload(ctx, t, logoPNG(t, 200, 200))
	if l, _ = f.svc.GetLogo(ctx); l.Current.ID != first.ID || l.Latest == nil || l.Latest.ID != second.ID {
		t.Fatalf("while processing: %+v", l)
	}
	f.process(ctx, t)
	if l, _ = f.svc.GetLogo(ctx); l.Current.ID != second.ID || l.Latest != nil {
		t.Fatalf("replaced: %+v", l)
	}
	want := []string{cur.ObjectKey, second.UploadKey}
	slices.Sort(want)
	if keys := f.deleted(ctx, t); !slices.Equal(keys, want) || f.objects.has(cur.ObjectKey) {
		t.Fatalf("deleted %q, want %q", keys, want)
	}

	// A bad upload fails with a reason and leaves the live logo alone.
	bad := f.upload(ctx, t, []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`))
	f.log.take()
	f.process(ctx, t)
	l, _ = f.svc.GetLogo(ctx)
	if l.Current.ID != second.ID || l.Latest == nil || l.Latest.ID != bad.ID || l.Latest.Status != domain.AssetFailed || l.Latest.FailureReason != "unsupported_type" {
		t.Fatalf("after a bad upload: %+v %+v", l.Current, l.Latest)
	}
	if ev, _ := f.log.take(); len(ev) != 0 {
		t.Fatalf("a rejected logo published %q", ev)
	}
	_, err = f.svc.CompleteLogoUpload(ctx, bad.ID)
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != "asset_failed" {
		t.Fatalf("complete a failed upload: %v", err)
	}
	f.deleted(ctx, t)

	// A new upload abandons the failed one.
	if _, _, err = f.svc.CreateLogoUpload(ctx, "image/webp"); err != nil {
		t.Fatal(err)
	}
	if keys := f.deleted(ctx, t); !slices.Contains(keys, bad.UploadKey) {
		t.Fatalf("abandoned upload not deleted: %q", keys)
	}

	if err = f.svc.DeleteLogo(ctx); err != nil {
		t.Fatal(err)
	}
	if l, _ = f.svc.GetLogo(ctx); l.Current != nil {
		t.Fatalf("logo still live: %+v", l.Current)
	}
	if ev, actions := f.log.take(); !slices.Contains(ev, domain.EventThemeChanged) || !slices.Contains(actions, "logo.deleted") {
		t.Fatalf("delete events %q actions %q", ev, actions)
	}
	if keys := f.deleted(ctx, t); len(keys) != 1 || !strings.HasPrefix(keys[0], storeA.ObjectPrefix()+"assets/") {
		t.Fatalf("deleted %q", keys)
	}
	if err = f.svc.DeleteLogo(ctx); !errors.Is(err, app.ErrNoLogo) {
		t.Fatalf("delete twice: %v", err)
	}
}

func TestLogoExpiryAndIsolation(t *testing.T) {
	t.Parallel()
	f := newAssetFixture(t)
	ctx := f.ctxA

	a, _, err := f.svc.CreateLogoUpload(ctx, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	// Store B can neither complete nor see A's upload.
	if _, err = f.svc.CompleteLogoUpload(f.ctxB, a.ID); !errors.Is(err, app.ErrAssetNotFound) {
		t.Fatalf("B completes A's upload: %v", err)
	}
	if l, _ := f.svc.GetLogo(f.ctxB); l.Latest != nil || l.Current != nil {
		t.Fatalf("B sees A's logo: %+v", l)
	}
	if err = f.svc.DeleteLogo(f.ctxB); !errors.Is(err, app.ErrNoLogo) {
		t.Fatalf("B deletes: %v", err)
	}

	for _, exp := range takeJobs[app.ExpireAssetUploadArgs](ctx, t, f) {
		if err = (&app.ExpireAssetUploadWorker{AssetWorkers: f.workers}).Work(ctx, &river.Job[app.ExpireAssetUploadArgs]{Args: exp}); err != nil {
			t.Fatal(err)
		}
	}
	if l, _ := f.svc.GetLogo(ctx); l.Latest != nil {
		t.Fatalf("expired upload still there: %+v", l.Latest)
	}
	if keys := f.deleted(ctx, t); !slices.Equal(keys, []string{a.UploadKey}) {
		t.Fatalf("deleted %q", keys)
	}
	if _, err = f.svc.CompleteLogoUpload(ctx, a.ID); !errors.Is(err, app.ErrAssetNotFound) {
		t.Fatalf("complete after expiry: %v", err)
	}

	// The delete job refuses another store's keys and deletes nothing.
	storeB, err := tenant.FromContext(f.ctxB)
	if err != nil {
		t.Fatal(err)
	}
	theirs := storeB.ObjectPrefix() + "assets/x.png"
	if err = f.objects.Put(ctx, theirs, "image/png", []byte("b")); err != nil {
		t.Fatal(err)
	}
	storeA, _ := tenant.FromContext(ctx)
	job := app.DeleteObjectsArgs{TenantArgs: jobs.TenantArgs{StoreID: storeA}, Keys: []string{theirs}}
	err = (&app.DeleteObjectsWorker{AssetWorkers: f.workers}).Work(ctx, &river.Job[app.DeleteObjectsArgs]{Args: job})
	var cancel *river.JobCancelError
	if !errors.As(err, &cancel) || !f.objects.has(theirs) {
		t.Fatalf("foreign delete: err %v, object kept %v", err, f.objects.has(theirs))
	}

	// A process job for an asset abandoned meanwhile does nothing.
	args := app.ProcessAssetArgs{TenantArgs: jobs.TenantArgs{StoreID: storeA}, AssetID: ulid.NewGenerator().New()}
	if err = (&app.ProcessAssetWorker{AssetWorkers: f.workers}).Work(ctx, &river.Job[app.ProcessAssetArgs]{Args: args}); err != nil {
		t.Fatalf("process abandoned: %v", err)
	}
}
