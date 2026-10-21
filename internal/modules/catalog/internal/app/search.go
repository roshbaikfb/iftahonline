package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/search"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/arabic"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// SearchIndex is the tenant-safe index (search.Client in production).
type SearchIndex interface {
	Upsert(ctx context.Context, docs []search.Doc) error
	Delete(ctx context.Context, ids []ulid.ID) error
	Search(ctx context.Context, q search.Query) (search.Result, error)
}

// Search limits.
const (
	MaxSearchTextLen   = 200
	DefaultSearchLimit = 20
	MaxSearchLimit     = 100
	MaxSearchOffset    = 1000
	searchTextBytes    = 4000 // of description and options, per document
)

// SearchQuery is a product search in the store in ctx.
type SearchQuery struct {
	Text       string
	Status     string
	CategoryID *ulid.ID
	Limit      int
	Offset     int
}

// SearchProducts searches the store's products. The text is normalized
// like the documents (Arabic forms, diacritics, digits); the store filter
// is added by the search client, never by the caller.
func (s *Service) SearchProducts(ctx context.Context, q SearchQuery) (search.Result, error) {
	var fields []errs.FieldError
	if utf8.RuneCountInString(q.Text) > MaxSearchTextLen || !utf8.ValidString(q.Text) {
		fields = append(fields, errs.FieldError{Field: "q", Code: "length", Message: fmt.Sprintf("q must be at most %d characters", MaxSearchTextLen)})
	}
	if q.Status != "" {
		if _, ok := domain.ParseProductStatus(q.Status); !ok {
			fields = append(fields, errs.FieldError{Field: "status", Code: "invalid", Message: "status must be draft, active or archived"})
		}
	}
	if q.Limit == 0 {
		q.Limit = DefaultSearchLimit
	}
	if q.Limit < 1 || q.Limit > MaxSearchLimit {
		fields = append(fields, errs.FieldError{Field: "limit", Code: "range", Message: fmt.Sprintf("limit must be between 1 and %d", MaxSearchLimit)})
	}
	if q.Offset < 0 || q.Offset > MaxSearchOffset {
		fields = append(fields, errs.FieldError{Field: "offset", Code: "range", Message: fmt.Sprintf("offset must be between 0 and %d", MaxSearchOffset)})
	}
	if len(fields) > 0 {
		return search.Result{}, errs.Validation(fields...)
	}
	res, err := s.search.Search(ctx, search.Query{
		Text: arabic.NormalizeSearch(q.Text), Status: q.Status, CategoryID: q.CategoryID, Limit: q.Limit, Offset: q.Offset,
	})
	if err != nil && !errors.Is(err, tenant.ErrNoTenant) {
		return search.Result{}, errs.Unavailable(time.Second, err)
	}
	return res, err
}

// IndexProductArgs is the product-changed event: the job brings the
// product's search document up to date (or removes it). It is inserted in
// the transaction of every change that affects the document.
type IndexProductArgs struct {
	jobs.TenantArgs
	ProductID ulid.ID `json:"product_id"`
}

// Kind implements river.JobArgs.
func (IndexProductArgs) Kind() string { return "catalog.index_product" }

func (s *Service) reindex(ctx context.Context, tx pgx.Tx, productID ulid.ID) error {
	return s.enqueue(ctx, tx, func(t jobs.TenantArgs) river.JobArgs { return IndexProductArgs{TenantArgs: t, ProductID: productID} }, nil)
}

// changed records in tx that a product changed: its search document is
// refreshed and catalog.product.changed is published.
func (s *Service) changed(ctx context.Context, tx pgx.Tx, productID ulid.ID, reason string) error {
	if err := s.reindex(ctx, tx, productID); err != nil {
		return err
	}
	return s.events.productChanged(ctx, tx, productID, reason)
}

func reindexTx(ctx context.Context, ins jobs.Inserter, tx pgx.Tx, productID ulid.ID) error {
	return enqueueTx(ctx, ins, tx, func(t jobs.TenantArgs) river.JobArgs { return IndexProductArgs{TenantArgs: t, ProductID: productID} }, nil)
}

// IndexWorker runs IndexProductArgs. It reads the product's current state
// when it runs, so jobs may repeat or arrive out of order.
type IndexWorker struct {
	river.WorkerDefaults[IndexProductArgs]
	DB    postgres.Beginner
	Index SearchIndex
}

// Work upserts the product's document, or deletes it when the product is
// gone.
func (w *IndexWorker) Work(ctx context.Context, job *river.Job[IndexProductArgs]) error {
	storeID, err := tenant.FromContext(ctx)
	if err != nil {
		return err
	}
	var p domain.Product
	err = postgres.InTenantTx(ctx, w.DB, func(tx pgx.Tx) (txErr error) {
		p, txErr = store.GetProduct(ctx, tx, job.Args.ProductID)
		return txErr
	})
	if errors.Is(err, domain.ErrNotFound) {
		return w.Index.Delete(ctx, []ulid.ID{job.Args.ProductID})
	}
	if err != nil {
		return err
	}
	return w.Index.Upsert(ctx, []search.Doc{ToDoc(storeID, p)})
}

// ToDoc builds a product's search document.
func ToDoc(storeID tenant.StoreID, p domain.Product) search.Doc {
	d := search.Doc{
		ID: p.ID.String(), StoreID: storeID.String(), Title: p.Title, Slug: p.Slug, Status: string(p.Status),
		CategoryIDs: make([]string, len(p.CategoryIDs)), CreatedAt: p.CreatedAt.Unix(),
		SearchTitle: arabic.NormalizeSearch(p.Title),
	}
	for i, c := range p.CategoryIDs {
		d.CategoryIDs[i] = c.String()
	}
	var text strings.Builder
	for _, o := range p.Options {
		text.WriteString(strings.Join(o.Values, " ") + " ")
	}
	for i, v := range p.Variants {
		if i == 0 || v.Price.Amount() < d.PriceMinor {
			d.PriceMinor, d.Currency = v.Price.Amount(), string(v.Price.Currency())
		}
		if v.SKU != "" {
			text.WriteString(v.SKU + " ")
		}
	}
	text.WriteString(p.Description)
	t := text.String()
	if len(t) > searchTextBytes {
		t = t[:searchTextBytes]
		for !utf8.ValidString(t) { // do not cut a rune in half
			t = t[:len(t)-1]
		}
	}
	d.SearchText = arabic.NormalizeSearch(t)
	for _, img := range p.Images {
		if img.Status == domain.ImageReady {
			d.ImageKey = img.ObjectKey
			break
		}
	}
	return d
}

// SearchSettingsArgs applies the index settings (global, periodic).
type SearchSettingsArgs struct {
	jobs.GlobalArgs
}

// Kind implements river.JobArgs.
func (SearchSettingsArgs) Kind() string { return "catalog.search_settings" }

// IndexEnsurer creates the index and applies its settings.
type IndexEnsurer interface {
	EnsureIndex(ctx context.Context) error
}

// SearchSettingsWorker runs SearchSettingsArgs.
type SearchSettingsWorker struct {
	river.WorkerDefaults[SearchSettingsArgs]
	Index IndexEnsurer
}

// Work implements river.Worker.
func (w *SearchSettingsWorker) Work(ctx context.Context, _ *river.Job[SearchSettingsArgs]) error {
	return w.Index.EnsureIndex(ctx)
}

// SearchSettingsPeriodic applies the settings when the worker starts and
// every six hours (a no-op when nothing changed).
func SearchSettingsPeriodic() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(6*time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return SearchSettingsArgs{}, nil },
		&river.PeriodicJobOpts{RunOnStart: true})
}
