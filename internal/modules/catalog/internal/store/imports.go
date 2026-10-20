package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/store/catalogdb"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

func importResult(r catalogdb.CatalogImport, err error, op string) (domain.Import, []byte, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Import{}, nil, domain.ErrNotFound
	}
	if err != nil {
		return domain.Import{}, nil, fmt.Errorf("catalog store: %s: %w", op, err)
	}
	im := domain.Import{
		ID: r.ID, Kind: domain.ImportKind(r.Kind), Status: domain.ImportStatus(r.Status), SourceKey: deref(r.SourceKey),
		WooURL: deref(r.WooUrl), Total: int(r.Total), Cursor: int(r.Cursor), Created: int(r.CreatedCount),
		Updated: int(r.UpdatedCount), Failed: int(r.FailedCount), FailureReason: r.FailureReason,
		CreatedAt: r.CreatedAt, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
	}
	if err := json.Unmarshal(r.RowErrors, &im.RowErrors); err != nil {
		return domain.Import{}, nil, fmt.Errorf("catalog store: decode row errors: %w", err)
	}
	return im, r.WooCredentials, nil
}

// NewImport is an import row to insert.
type NewImport struct {
	ID             ulid.ID
	Kind           domain.ImportKind
	SourceKey      string
	WooURL         string
	WooCredentials []byte // sealed
}

// InsertImport creates a pending import.
func InsertImport(ctx context.Context, tx Tx, in NewImport) (domain.Import, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Import{}, err
	}
	p := catalogdb.InsertImportParams{ID: in.ID, StoreID: store, Kind: string(in.Kind), WooCredentials: in.WooCredentials}
	if in.SourceKey != "" {
		p.SourceKey = &in.SourceKey
	}
	if in.WooURL != "" {
		p.WooUrl = &in.WooURL
	}
	row, err := catalogdb.New(tx).InsertImport(ctx, p)
	im, _, err := importResult(row, err, "insert import")
	return im, err
}

// GetImport returns an import and its sealed WooCommerce credentials.
func GetImport(ctx context.Context, tx Tx, id ulid.ID) (domain.Import, []byte, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Import{}, nil, err
	}
	row, err := catalogdb.New(tx).GetImport(ctx, catalogdb.GetImportParams{StoreID: store, ID: id})
	return importResult(row, err, "get import")
}

// ListImports returns the store's latest imports, newest first.
func ListImports(ctx context.Context, tx Tx, limit int) ([]domain.Import, error) {
	store, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := catalogdb.New(tx).ListImports(ctx, catalogdb.ListImportsParams{StoreID: store, Lim: int32(limit)}) //nolint:gosec // small constant
	if err != nil {
		return nil, fmt.Errorf("catalog store: list imports: %w", err)
	}
	out := make([]domain.Import, len(rows))
	for i, r := range rows {
		if out[i], _, err = importResult(r, nil, "list imports"); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// QueueImport moves a pending import to queued; ErrNotFound otherwise.
func QueueImport(ctx context.Context, tx Tx, id ulid.ID) (domain.Import, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Import{}, err
	}
	row, err := catalogdb.New(tx).QueueImport(ctx, catalogdb.QueueImportParams{StoreID: store, ID: id})
	im, _, err := importResult(row, err, "queue import")
	return im, err
}

// StartImport marks a queued (or resumed) import running with its total.
func StartImport(ctx context.Context, tx Tx, id ulid.ID, total int) (domain.Import, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Import{}, err
	}
	row, err := catalogdb.New(tx).StartImport(ctx, catalogdb.StartImportParams{StoreID: store, ID: id, Total: int32(total)}) //nolint:gosec // bounded by domain.MaxImportRecords
	im, _, err := importResult(row, err, "start import")
	return im, err
}

// Progress is a running import's counters.
type Progress struct {
	Cursor, Created, Updated, Failed int
	RowErrors                        []domain.RowError
}

// SaveProgress records how far a running import got.
func SaveProgress(ctx context.Context, tx Tx, id ulid.ID, p Progress) error {
	store, err := current(ctx)
	if err != nil {
		return err
	}
	rowErrors, err := json.Marshal(p.RowErrors)
	if err != nil {
		return fmt.Errorf("catalog store: encode row errors: %w", err)
	}
	if p.RowErrors == nil {
		rowErrors = []byte("[]")
	}
	err = catalogdb.New(tx).SaveImportProgress(ctx, catalogdb.SaveImportProgressParams{
		StoreID: store, ID: id, Cursor: int32(p.Cursor), CreatedCount: int32(p.Created), //nolint:gosec // bounded by domain.MaxImportRecords
		UpdatedCount: int32(p.Updated), FailedCount: int32(p.Failed), RowErrors: rowErrors, //nolint:gosec // bounded by domain.MaxImportRecords
	})
	if err != nil {
		return fmt.Errorf("catalog store: save progress: %w", err)
	}
	return nil
}

// FinishImport ends an import as succeeded or failed.
func FinishImport(ctx context.Context, tx Tx, id ulid.ID, status domain.ImportStatus, reason string) (domain.Import, error) {
	store, err := current(ctx)
	if err != nil {
		return domain.Import{}, err
	}
	row, err := catalogdb.New(tx).FinishImport(ctx, catalogdb.FinishImportParams{StoreID: store, ID: id, Status: string(status), FailureReason: reason})
	im, _, err := importResult(row, err, "finish import")
	return im, err
}

// ProductIDBySlug finds a live product by slug.
func ProductIDBySlug(ctx context.Context, tx Tx, slug string) (ulid.ID, error) {
	store, err := current(ctx)
	if err != nil {
		return ulid.ID{}, err
	}
	id, err := catalogdb.New(tx).GetProductIDBySlug(ctx, catalogdb.GetProductIDBySlugParams{StoreID: store, Slug: slug})
	if errors.Is(err, pgx.ErrNoRows) {
		return ulid.ID{}, domain.ErrNotFound
	}
	if err != nil {
		return ulid.ID{}, fmt.Errorf("catalog store: product by slug: %w", err)
	}
	return id, nil
}

// CategoryIDBySlug finds a category by slug.
func CategoryIDBySlug(ctx context.Context, tx Tx, slug string) (ulid.ID, error) {
	store, err := current(ctx)
	if err != nil {
		return ulid.ID{}, err
	}
	id, err := catalogdb.New(tx).GetCategoryIDBySlug(ctx, catalogdb.GetCategoryIDBySlugParams{StoreID: store, Slug: slug})
	if errors.Is(err, pgx.ErrNoRows) {
		return ulid.ID{}, domain.ErrNotFound
	}
	if err != nil {
		return ulid.ID{}, fmt.Errorf("catalog store: category by slug: %w", err)
	}
	return id, nil
}
