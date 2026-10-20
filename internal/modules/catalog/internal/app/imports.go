package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/importer"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/media"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/retry"
	"github.com/roshbaikfb/iftahonline/internal/platform/safehttp"
	"github.com/roshbaikfb/iftahonline/internal/platform/secretbox"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Imports configures the importers.
type Imports struct {
	// Keyring seals WooCommerce credentials at rest.
	Keyring *secretbox.Keyring
	// HTTP calls merchants' shops; it must be a platform/safehttp client
	// built with URLRules.
	HTTP *http.Client
	// URLRules checks shop URLs when an import is created (the zero value
	// in production; tests re-allow loopback).
	URLRules safehttp.Options
	// AllowHTTP permits http:// shop URLs (tests only).
	AllowHTTP bool
	Retry     retry.Policy
}

// progressEvery is how many records pass between saved checkpoints.
const progressEvery = 25

var (
	errImportNotFound = errs.NotFound("import_not_found", "import not found")
	errImportStarted  = errs.Conflict("import_already_started", "this import has already started")
)

// NewImportInput starts an import.
type NewImportInput struct {
	Kind      string
	WooURL    string
	WooKey    string
	WooSecret string
}

type wooCredentials struct {
	Key    string `json:"key"`
	Secret string `json:"secret"`
}

// CreateImport creates a pending import. File imports get a presigned
// upload for the file; WooCommerce imports store the shop URL and the
// sealed API keys. Call StartImport next.
func (s *Service) CreateImport(ctx context.Context, in NewImportInput) (domain.Import, *media.Upload, error) {
	kind, ok := domain.ParseImportKind(in.Kind)
	if !ok {
		return domain.Import{}, nil, errs.Validation(errs.FieldError{Field: "kind", Code: "invalid", Message: "kind must be csv or woocommerce"})
	}
	storeID, err := tenant.FromContext(ctx)
	if err != nil {
		return domain.Import{}, nil, fmt.Errorf("catalog: %w", err)
	}
	id := s.ids.New()
	row := store.NewImport{ID: id, Kind: kind}
	if kind == domain.ImportWooCommerce {
		if row.WooURL, err = s.validateWoo(in); err != nil {
			return domain.Import{}, nil, err
		}
		creds, encErr := json.Marshal(wooCredentials{Key: strings.TrimSpace(in.WooKey), Secret: strings.TrimSpace(in.WooSecret)})
		if encErr != nil {
			return domain.Import{}, nil, fmt.Errorf("catalog: encode credentials: %w", encErr)
		}
		if row.WooCredentials, err = s.imports.Keyring.Seal(creds, id[:]); err != nil {
			return domain.Import{}, nil, fmt.Errorf("catalog: seal credentials: %w", err)
		}
	} else {
		row.SourceKey = domain.ImportKey(storeID, id)
	}
	var im domain.Import
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		if im, txErr = store.InsertImport(ctx, tx, row); txErr != nil {
			return txErr
		}
		return s.audit.RecordTx(ctx, tx, auditEntry("import.created", "import", id))
	})
	if err != nil || kind == domain.ImportWooCommerce {
		return im, nil, err
	}
	up, err := s.media.Objects.PresignUpload(ctx, row.SourceKey, kind.UploadContentType(), domain.MaxImportFileBytes, domain.UploadURLTTL)
	if err != nil {
		return domain.Import{}, nil, err
	}
	return im, &up, nil
}

func (s *Service) validateWoo(in NewImportInput) (string, error) {
	var fields []errs.FieldError
	raw := strings.TrimRight(strings.TrimSpace(in.WooURL), "/")
	u, err := safehttp.ValidateURL(raw, s.imports.URLRules)
	if err != nil || (u.Scheme != "https" && !s.imports.AllowHTTP) || u.RawQuery != "" || u.Fragment != "" {
		fields = append(fields, errs.FieldError{Field: "woocommerce.url", Code: "invalid", Message: "use the shop's https:// address"})
	}
	if k := strings.TrimSpace(in.WooKey); !strings.HasPrefix(k, "ck_") || len(k) > 200 {
		fields = append(fields, errs.FieldError{Field: "woocommerce.consumer_key", Code: "invalid", Message: "a WooCommerce consumer key starts with ck_"})
	}
	if sec := strings.TrimSpace(in.WooSecret); !strings.HasPrefix(sec, "cs_") || len(sec) > 200 {
		fields = append(fields, errs.FieldError{Field: "woocommerce.consumer_secret", Code: "invalid", Message: "a WooCommerce consumer secret starts with cs_"})
	}
	if len(fields) > 0 {
		return "", errs.Validation(fields...)
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String(), nil
}

// StartImport queues a pending import's job (after the file upload).
func (s *Service) StartImport(ctx context.Context, id ulid.ID) (domain.Import, error) {
	var im domain.Import
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		im, err = store.QueueImport(ctx, tx, id)
		if errors.Is(err, domain.ErrNotFound) {
			if _, _, getErr := store.GetImport(ctx, tx, id); getErr != nil {
				return getErr
			}
			return errImportStarted
		}
		if err != nil {
			return err
		}
		return s.enqueue(ctx, tx, func(t jobs.TenantArgs) river.JobArgs { return RunImportArgs{TenantArgs: t, ImportID: id} }, nil)
	})
	return im, notFound(err, errImportNotFound)
}

// GetImport returns an import with its progress.
func (s *Service) GetImport(ctx context.Context, id ulid.ID) (domain.Import, error) {
	var im domain.Import
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		im, _, txErr = store.GetImport(ctx, tx, id)
		return txErr
	})
	return im, notFound(err, errImportNotFound)
}

// ListImports returns the store's 50 latest imports.
func (s *Service) ListImports(ctx context.Context) ([]domain.Import, error) {
	var out []domain.Import
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		out, txErr = store.ListImports(ctx, tx, 50)
		return txErr
	})
	return out, err
}

// RunImportArgs imports every record of an import's source.
type RunImportArgs struct {
	jobs.TenantArgs
	ImportID ulid.ID `json:"import_id"`
}

// Kind implements river.JobArgs.
func (RunImportArgs) Kind() string { return "catalog.run_import" }

// InsertOpts allows a few retries; each resumes from the checkpoint.
func (RunImportArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 5} }

// ImportWorker runs RunImportArgs.
type ImportWorker struct {
	river.WorkerDefaults[RunImportArgs]
	Svc *Service
}

// Timeout bounds one attempt; a retry resumes where it stopped.
func (*ImportWorker) Timeout(*river.Job[RunImportArgs]) time.Duration { return 30 * time.Minute }

// Work imports the records after the saved cursor. A source that cannot
// be read ends the import as failed with a reason code; a transient error
// returns, and River retries from the checkpoint.
func (w *ImportWorker) Work(ctx context.Context, job *river.Job[RunImportArgs]) error {
	return w.Svc.runImport(ctx, job.Args.ImportID)
}

func (s *Service) runImport(ctx context.Context, id ulid.ID) error {
	var im domain.Import
	var sealed []byte
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		im, sealed, txErr = store.GetImport(ctx, tx, id)
		return txErr
	})
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if im.Status != domain.ImportQueued && im.Status != domain.ImportRunning {
		return nil
	}
	src, err := s.openSource(ctx, im, sealed)
	if code, bad := importer.IsSourceError(err); bad {
		return s.finishImport(ctx, im, domain.ImportFailed, code)
	}
	if err != nil {
		return err
	}
	if err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		im, txErr = store.StartImport(ctx, tx, id, max(src.Total(), 0))
		return txErr
	}); err != nil {
		return err
	}
	for range im.Cursor {
		if err = src.Skip(ctx); err != nil {
			return s.sourceFailure(ctx, im, err)
		}
	}
	p := store.Progress{Cursor: im.Cursor, Created: im.Created, Updated: im.Updated, Failed: im.Failed, RowErrors: im.RowErrors}
	cats := map[string]ulid.ID{}
	for {
		var rec domain.ImportRecord
		rec, err = src.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if saveErr := s.saveProgress(ctx, id, p); saveErr != nil {
				return errors.Join(err, saveErr)
			}
			return s.sourceFailure(ctx, im, err)
		}
		created, msg := s.importRecord(ctx, rec, cats)
		switch {
		case msg != "":
			p.Failed++
			if len(p.RowErrors) < domain.MaxImportRowErrors {
				p.RowErrors = append(p.RowErrors, domain.RowError{Line: rec.Line, Message: msg})
			}
		case created:
			p.Created++
		default:
			p.Updated++
		}
		p.Cursor++
		if p.Cursor%progressEvery == 0 {
			if err = s.saveProgress(ctx, id, p); err != nil {
				return err
			}
		}
	}
	if err = s.saveProgress(ctx, id, p); err != nil {
		return err
	}
	return s.finishImport(ctx, im, domain.ImportSucceeded, "")
}

// sourceFailure ends the import for a bad source, or returns a transient
// error for River to retry.
func (s *Service) sourceFailure(ctx context.Context, im domain.Import, err error) error {
	if code, bad := importer.IsSourceError(err); bad {
		return s.finishImport(ctx, im, domain.ImportFailed, code)
	}
	if errors.Is(err, io.EOF) {
		return s.finishImport(ctx, im, domain.ImportFailed, "source_changed")
	}
	return err
}

func (s *Service) saveProgress(ctx context.Context, id ulid.ID, p store.Progress) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error { return store.SaveProgress(ctx, tx, id, p) })
}

// finishImport records the outcome and removes an uploaded file.
func (s *Service) finishImport(ctx context.Context, im domain.Import, status domain.ImportStatus, reason string) error {
	return postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := store.FinishImport(ctx, tx, im.ID, status, reason); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		if err := s.audit.RecordTx(ctx, tx, auditEntry("import.finished", "import", im.ID)); err != nil {
			return err
		}
		if im.SourceKey == "" {
			return nil
		}
		return s.deleteObjects(ctx, tx, []string{im.SourceKey})
	})
}

func (s *Service) openSource(ctx context.Context, im domain.Import, sealed []byte) (importer.Source, error) {
	cur, err := s.currency.StoreCurrency(ctx)
	if err != nil {
		return nil, fmt.Errorf("catalog: store currency: %w", err)
	}
	switch im.Kind {
	case domain.ImportCSV:
		data, err := s.media.Objects.Get(ctx, im.SourceKey, domain.MaxImportFileBytes)
		if errors.Is(err, media.ErrNoObject) {
			return nil, &importer.SourceError{Code: "upload_missing", Detail: "the file was not uploaded"}
		}
		if err != nil {
			return nil, err
		}
		return importer.NewCSV(data, cur)
	case domain.ImportWooCommerce:
		plain, err := s.imports.Keyring.Open(sealed, im.ID[:])
		if err != nil {
			return nil, &importer.SourceError{Code: "credentials_unreadable", Detail: "start a new import"}
		}
		var c wooCredentials
		if err = json.Unmarshal(plain, &c); err != nil {
			return nil, &importer.SourceError{Code: "credentials_unreadable", Detail: "start a new import"}
		}
		return importer.OpenWoo(ctx, importer.WooConfig{
			BaseURL: im.WooURL, Key: c.Key, Secret: c.Secret, Client: s.imports.HTTP, Currency: cur,
			AllowHTTP: s.imports.AllowHTTP, Retry: s.imports.Retry,
		})
	}
	return nil, &importer.SourceError{Code: "unknown_kind", Detail: string(im.Kind)}
}

// importRecord creates the record's product, or replaces the product with
// the same slug (variants matched by SKU keep their identity and stock).
// It reports whether a product was created, or a message for the merchant.
func (s *Service) importRecord(ctx context.Context, rec domain.ImportRecord, cats map[string]ulid.ID) (created bool, msg string) {
	if rec.Err != "" {
		return false, rec.Err
	}
	in, err := domain.ValidateProduct(rec.Product)
	if err != nil {
		return false, describe(err)
	}
	for _, name := range rec.Categories {
		id, catErr := s.categoryFor(ctx, name, cats)
		if catErr != nil {
			return false, describe(catErr)
		}
		in.CategoryIDs = append(in.CategoryIDs, id)
	}
	var existing domain.Product
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		pid, txErr := store.ProductIDBySlug(ctx, tx, in.Slug)
		if txErr != nil {
			return txErr
		}
		existing, txErr = store.GetProduct(ctx, tx, pid)
		return txErr
	})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		if _, err = s.CreateProduct(ctx, in); err != nil {
			return false, describe(err)
		}
		return true, ""
	case err != nil:
		return false, describe(err)
	}
	bySKU := make(map[string]ulid.ID, len(existing.Variants))
	for _, v := range existing.Variants {
		if v.SKU != "" {
			bySKU[strings.ToLower(v.SKU)] = v.ID
		}
	}
	for i := range in.Variants {
		if id, ok := bySKU[strings.ToLower(in.Variants[i].SKU)]; ok && in.Variants[i].SKU != "" {
			in.Variants[i].ID = &id
		}
	}
	if _, err = s.UpdateProduct(ctx, existing.ID, in); err != nil {
		return false, describe(err)
	}
	return false, ""
}

// categoryFor finds a category by the slug of name, or creates it as a
// root category. Results are cached for the run.
func (s *Service) categoryFor(ctx context.Context, name string, cache map[string]ulid.ID) (ulid.ID, error) {
	slug := domain.Slugify(name)
	if id, ok := cache[slug]; ok {
		return id, nil
	}
	var id ulid.ID
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		id, txErr = store.CategoryIDBySlug(ctx, tx, slug)
		return txErr
	})
	if errors.Is(err, domain.ErrNotFound) {
		var c domain.Category
		if c, err = s.CreateCategory(ctx, domain.CategoryInput{Name: name, Slug: slug}); err == nil {
			id = c.ID
		}
	}
	if err != nil {
		return ulid.ID{}, err
	}
	cache[slug] = id
	return id, nil
}

// describe turns an error into a message for the import report.
func describe(err error) string {
	var e *errs.Error
	if errors.As(err, &e) && len(e.Fields) > 0 {
		var b bytes.Buffer
		for i, f := range e.Fields {
			if i > 0 {
				b.WriteString("; ")
			}
			b.WriteString(f.Field + ": " + f.Message)
		}
		return b.String()
	}
	if errors.As(err, &e) {
		return e.Message
	}
	return "could not save the product"
}
