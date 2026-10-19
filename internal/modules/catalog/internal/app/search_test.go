package app_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/search"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// memIndex is an in-memory index keyed by store, with the same contract
// as search.Client: the store comes from ctx.
type memIndex struct {
	mu      sync.Mutex
	docs    map[string]map[string]search.Doc // store → id → doc
	queries []search.Query
	fail    error
}

func newMemIndex() *memIndex { return &memIndex{docs: map[string]map[string]search.Doc{}} }

func (m *memIndex) Upsert(ctx context.Context, docs []search.Doc) error {
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.docs[id.String()] == nil {
		m.docs[id.String()] = map[string]search.Doc{}
	}
	for _, d := range docs {
		if d.StoreID != id.String() {
			return search.ErrForeignDoc
		}
		m.docs[id.String()][d.ID] = d
	}
	return nil
}

func (m *memIndex) Delete(ctx context.Context, ids []ulid.ID) error {
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range ids {
		delete(m.docs[id.String()], d.String())
	}
	return nil
}

func (m *memIndex) Search(ctx context.Context, q search.Query) (search.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queries = append(m.queries, q)
	if m.fail != nil {
		return search.Result{}, m.fail
	}
	if _, err := tenant.FromContext(ctx); err != nil {
		return search.Result{}, err
	}
	return search.Result{Hits: []search.Doc{}}, nil
}

func (m *memIndex) doc(ctx context.Context, t *testing.T, id ulid.ID) (search.Doc, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.docs[mustStore(ctx, t).String()][id.String()]
	return d, ok
}

func runIndexJobs(ctx context.Context, t *testing.T, f fixture) int {
	t.Helper()
	jobs := takeJobs[app.IndexProductArgs](ctx, t, f)
	for _, a := range jobs {
		if err := (&app.IndexWorker{DB: f.pool, Index: f.index}).Work(ctx, &river.Job[app.IndexProductArgs]{Args: a}); err != nil {
			t.Fatalf("index: %v", err)
		}
	}
	return len(jobs)
}

func TestIndexingFollowsProductChanges(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := f.ctxA
	cat, err := f.svc.CreateCategory(ctx, domain.CategoryInput{Name: "Shoes"})
	if err != nil {
		t.Fatal(err)
	}
	in := tee("أحذية رياضية", 3)
	in.Description = "جلد طبيعي"
	in.CategoryIDs = []ulid.ID{cat.ID}
	p := mustCreate(ctx, t, f, in)
	if n := runIndexJobs(ctx, t, f); n != 1 {
		t.Fatalf("create enqueued %d index jobs, want 1", n)
	}
	d, ok := f.index.doc(ctx, t, p.ID)
	if !ok {
		t.Fatal("product not indexed")
	}
	if d.StoreID != mustStore(ctx, t).String() || d.Title != "أحذية رياضية" || d.SearchTitle != "احذيه رياضيه" ||
		d.PriceMinor != 15000 || d.Currency != "EGP" || d.Status != "active" || len(d.CategoryIDs) != 1 ||
		!strings.Contains(d.SearchText, "جلد طبيعي") || !strings.Contains(d.SearchText, "احذيه رياضيه-s") {
		t.Fatalf("doc = %+v", d)
	}

	upd := in
	upd.Title = "حذاء جري"
	upd.Variants = []domain.VariantInput{{ID: &p.Variants[0].ID, SKU: "RUN-1", OptionValues: []string{"S"}, PriceMinor: 9000}}
	if _, err = f.svc.UpdateProduct(ctx, p.ID, upd); err != nil {
		t.Fatal(err)
	}
	runIndexJobs(ctx, t, f)
	if d, _ = f.index.doc(ctx, t, p.ID); d.Title != "حذاء جري" || d.PriceMinor != 9000 || !strings.Contains(d.SearchText, "run-1") {
		t.Fatalf("doc after update = %+v", d)
	}

	// A ready image becomes the thumbnail.
	img, _, err := f.svc.CreateImageUpload(ctx, p.ID, "image/jpeg", domain.ImageInput{})
	if err != nil {
		t.Fatal(err)
	}
	_ = f.objects.Put(ctx, img.UploadKey, "image/jpeg", photo(t, 20, 20))
	if _, err = f.svc.CompleteImageUpload(ctx, p.ID, img.ID); err != nil {
		t.Fatal(err)
	}
	runJobs(ctx, t, f)
	runIndexJobs(ctx, t, f)
	if d, _ = f.index.doc(ctx, t, p.ID); d.ImageKey != domain.MasterKey(mustStore(ctx, t), img.ID, "jpeg") {
		t.Fatalf("thumbnail = %q", d.ImageKey)
	}

	if err = f.svc.DeleteProduct(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	runIndexJobs(ctx, t, f)
	if _, ok = f.index.doc(ctx, t, p.ID); ok {
		t.Fatal("deleted product still indexed")
	}
}

func TestSearchProducts(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	cat := f.ids.New()
	if _, err := f.svc.SearchProducts(f.ctxA, app.SearchQuery{Text: "  أحذيةٌ  ", Status: "active", CategoryID: &cat, Offset: 20}); err != nil {
		t.Fatal(err)
	}
	q := f.index.queries[len(f.index.queries)-1]
	if q.Text != "احذيه" || q.Limit != app.DefaultSearchLimit || q.Status != "active" || *q.CategoryID != cat || q.Offset != 20 {
		t.Fatalf("query passed down = %+v", q)
	}
	_, err := f.svc.SearchProducts(f.ctxA, app.SearchQuery{Text: strings.Repeat("x", 201), Status: "gone", Limit: 101, Offset: 1001})
	for _, field := range []string{"q", "status", "limit", "offset"} {
		wantField(t, err, field, map[string]string{"q": "length", "status": "invalid", "limit": "range", "offset": "range"}[field])
	}
	f.index.fail = errors.New("meili down")
	if _, err = f.svc.SearchProducts(f.ctxA, app.SearchQuery{}); !errs.Is(err, errs.KindUnavailable) {
		t.Fatalf("index down: %v", err)
	}
	f.index.fail = nil
	if _, err = f.svc.SearchProducts(context.Background(), app.SearchQuery{}); !errors.Is(err, tenant.ErrNoTenant) {
		t.Fatalf("no tenant: %v", err)
	}
}

func TestIndexWorkerCrossTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	pb := mustCreate(f.ctxB, t, f, single("B only", 1, true))
	runIndexJobs(f.ctxB, t, f)
	// A job for B's product running as A finds nothing (RLS) and only
	// deletes in A's own index space: B's document survives.
	args := app.IndexProductArgs{ProductID: pb.ID}
	args.StoreID = mustStore(f.ctxA, t)
	if err := (&app.IndexWorker{DB: f.pool, Index: f.index}).Work(f.ctxA, &river.Job[app.IndexProductArgs]{Args: args}); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.index.doc(f.ctxB, t, pb.ID); !ok {
		t.Fatal("store A's job removed store B's document")
	}
	if _, ok := f.index.doc(f.ctxA, t, pb.ID); ok {
		t.Fatal("B's product indexed under A")
	}
}

func TestToDocTruncatesSafely(t *testing.T) {
	t.Parallel()
	p := domain.Product{ID: ulid.NewGenerator().New(), Title: "x", Description: strings.Repeat("ب", 3000)}
	d := app.ToDoc(tenant.StoreID{ID: p.ID}, p)
	if len(d.SearchText) > 4000 || !strings.HasPrefix(d.SearchText, "ب") || strings.ContainsRune(d.SearchText, '�') {
		t.Fatalf("search text: %d bytes", len(d.SearchText))
	}
}
