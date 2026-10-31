package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/store/tenancydb"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// The functions in this file are the cross-tenant store directory for the
// super-admin console. They take no tenant from the context: they must
// only ever run on the app_admin pool (ADR-0007 §5), whose policies allow
// reading every store.

// DirectoryEntry is a store as the console lists it.
type DirectoryEntry struct {
	Store       domain.Store
	PrimaryHost string
}

// DirectoryFilter narrows a search. Query matches a slug prefix or a name
// substring, case-insensitively.
type DirectoryFilter struct {
	Query    string
	Status   string
	BeforeAt *time.Time
	BeforeID *ulid.ID
	Limit    int
}

// likeEscape makes user text literal inside LIKE/ILIKE patterns.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// SearchDirectory lists stores across tenants, newest first.
func SearchDirectory(ctx context.Context, adminDB Tx, f DirectoryFilter) ([]DirectoryEntry, error) {
	rows, err := tenancydb.New(adminDB).AdminSearchStores(ctx, tenancydb.AdminSearchStoresParams{
		Query: likeEscape(strings.ToLower(strings.TrimSpace(f.Query))), Status: f.Status,
		BeforeAt: f.BeforeAt, BeforeID: f.BeforeID, Lim: int32(f.Limit), //nolint:gosec // bounded by the caller.
	})
	if err != nil {
		return nil, fmt.Errorf("tenancy store: directory: %w", err)
	}
	out := make([]DirectoryEntry, len(rows))
	for i, r := range rows {
		out[i] = DirectoryEntry{PrimaryHost: r.PrimaryHost, Store: toStore(tenancydb.Store{
			ID: r.ID, Slug: r.Slug, Name: r.Name, Status: r.Status, Currency: r.Currency,
			Locale: r.Locale, Timezone: r.Timezone, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, BillingHold: r.BillingHold,
		})}
	}
	return out, nil
}

// DirectoryStore returns one store and its domains.
func DirectoryStore(ctx context.Context, adminDB Tx, id tenant.StoreID) (domain.Store, []domain.Domain, error) {
	q := tenancydb.New(adminDB)
	row, err := q.AdminGetStore(ctx, id.ID)
	st, err := storeResult(row, err, "directory store")
	if err != nil {
		return domain.Store{}, nil, err
	}
	rows, err := q.AdminListDomains(ctx, id.ID)
	if err != nil {
		return domain.Store{}, nil, fmt.Errorf("tenancy store: directory domains: %w", err)
	}
	ds := make([]domain.Domain, len(rows))
	for i, r := range rows {
		ds[i] = toDomain(r)
	}
	return st, ds, nil
}

// DirectoryCounts counts every store by state (the health page).
func DirectoryCounts(ctx context.Context, adminDB Tx) (domain.StoreCounts, error) {
	r, err := tenancydb.New(adminDB).AdminStoreCounts(ctx)
	if err != nil {
		return domain.StoreCounts{}, fmt.Errorf("tenancy store: directory counts: %w", err)
	}
	return domain.StoreCounts{Active: r.Active, Held: r.Held, Suspended: r.Suspended, Closed: r.Closed, Total: r.Total}, nil
}
