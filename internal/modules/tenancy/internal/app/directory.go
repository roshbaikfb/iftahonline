package app

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// StoreDirectory is the super-admin console's read-only view of every store.
// It runs on the app_admin pool only.
type StoreDirectory struct{ db store.Tx }

// NewStoreDirectory returns a directory over the admin pool.
func NewStoreDirectory(adminDB store.Tx) *StoreDirectory { return &StoreDirectory{db: adminDB} }

// DirectoryPage is one page of the store directory.
type DirectoryPage struct {
	Items      []store.DirectoryEntry
	NextCursor string
}

// Directory page sizes.
const (
	DefaultDirectoryLimit = 50
	MaxDirectoryLimit     = 100
)

var errBadDirectoryQuery = errs.Validation(errs.FieldError{Field: "query", Code: "invalid", Message: "invalid search"})

// Search lists stores, newest first, optionally filtered.
func (d *StoreDirectory) Search(ctx context.Context, query, status, cursor string, limit int) (DirectoryPage, error) {
	if limit <= 0 {
		limit = DefaultDirectoryLimit
	}
	if limit > MaxDirectoryLimit || len(query) > 100 {
		return DirectoryPage{}, errBadDirectoryQuery
	}
	if status != "" {
		if _, ok := domain.ParseStoreStatus(status); !ok {
			return DirectoryPage{}, errs.Validation(errs.FieldError{Field: "status", Code: "invalid", Message: "unknown status"})
		}
	}
	f := store.DirectoryFilter{Query: query, Status: status, Limit: limit + 1}
	if cursor != "" {
		at, id, err := decodeDirCursor(cursor)
		if err != nil {
			return DirectoryPage{}, errs.Validation(errs.FieldError{Field: "cursor", Code: "invalid", Message: "invalid cursor"})
		}
		f.BeforeAt, f.BeforeID = &at, &id
	}
	items, err := store.SearchDirectory(ctx, d.db, f)
	if err != nil {
		return DirectoryPage{}, err
	}
	page := DirectoryPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1].Store
		page.NextCursor = encodeDirCursor(last.CreatedAt, last.ID.ID)
	}
	return page, nil
}

// Store returns one store and its domains.
func (d *StoreDirectory) Store(ctx context.Context, id tenant.StoreID) (domain.Store, []domain.Domain, error) {
	st, ds, err := store.DirectoryStore(ctx, d.db, id)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Store{}, nil, errStoreNotFound
	}
	return st, ds, err
}

func encodeDirCursor(at time.Time, id ulid.ID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(at.UnixMicro(), 10) + ":" + id.String()))
}

func decodeDirCursor(c string) (time.Time, ulid.ID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, ulid.ID{}, err
	}
	us, ids, ok := strings.Cut(string(raw), ":")
	if !ok {
		return time.Time{}, ulid.ID{}, fmt.Errorf("tenancy: cursor")
	}
	n, err := strconv.ParseInt(us, 10, 64)
	if err != nil {
		return time.Time{}, ulid.ID{}, err
	}
	id, err := ulid.Parse(ids)
	return time.UnixMicro(n).UTC(), id, err
}

// Counts are the platform's stores by state (the health page, ADR-0058).
func (d *StoreDirectory) Counts(ctx context.Context) (domain.StoreCounts, error) {
	return store.DirectoryCounts(ctx, d.db)
}

// consoleZone is the zone the console counts days in: the owner's.
const consoleZone = "Africa/Cairo"

// NewStores are the stores created in the last 30 Cairo days up to now
// (the console's home, ADR-0075).
func (d *StoreDirectory) NewStores(ctx context.Context, now time.Time) (domain.NewStores, error) {
	loc, err := time.LoadLocation(consoleZone)
	if err != nil {
		return domain.NewStores{}, fmt.Errorf("tenancy: zone: %w", err)
	}
	local := now.In(loc)
	first := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1-domain.NewStoresDays)
	byDay, err := store.NewStoresByDay(ctx, d.db, first, consoleZone)
	if err != nil {
		return domain.NewStores{}, err
	}
	return domain.NewStoresFrom(byDay, local), nil
}

// Entries are the stores with these IDs, newest first, in one query (the
// console's search, ADR-0079); an unknown ID is left out.
func (d *StoreDirectory) Entries(ctx context.Context, ids []ulid.ID) ([]store.DirectoryEntry, error) {
	if len(ids) == 0 {
		return []store.DirectoryEntry{}, nil
	}
	return store.DirectoryByID(ctx, d.db, ids)
}

// Names maps store IDs to their names (the admin log), in one query.
func (d *StoreDirectory) Names(ctx context.Context, ids []ulid.ID) (map[ulid.ID]string, error) {
	if len(ids) == 0 {
		return map[ulid.ID]string{}, nil
	}
	return store.Names(ctx, d.db, ids)
}
