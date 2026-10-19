// Package store adapts the sqlc queries in tenancydb to domain types. Every
// function runs on a transaction opened by postgres.InTenantTx and takes
// the store ID from tenant.FromContext, never from its arguments
// (CLAUDE.md rule 5); RLS checks it a second time.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/store/tenancydb"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Tx is the tenant transaction the functions run on.
type Tx = tenancydb.DBTX

func current(ctx context.Context) (ulid.ID, error) {
	id, err := tenant.FromContext(ctx)
	if err != nil {
		return ulid.ID{}, fmt.Errorf("tenancy store: %w", err)
	}
	return id.ID, nil
}

// InsertStore creates the store whose ID is the tenant in ctx.
func InsertStore(ctx context.Context, tx Tx, in domain.NewStore) (domain.Store, error) {
	id, err := current(ctx)
	if err != nil {
		return domain.Store{}, err
	}
	row, err := tenancydb.New(tx).InsertStore(ctx, tenancydb.InsertStoreParams{
		ID: id, Slug: in.Slug, Name: in.Name, Currency: in.Currency, Locale: in.Locale, Timezone: in.Timezone,
	})
	if err != nil {
		if postgres.IsUniqueViolation(err, "stores_slug_key") {
			return domain.Store{}, errs.Validation(errs.FieldError{Field: "slug", Code: "taken", Message: "this slug is already taken"})
		}
		return domain.Store{}, fmt.Errorf("tenancy store: insert store: %w", err)
	}
	return toStore(row), nil
}

// GetStore returns the current store.
func GetStore(ctx context.Context, tx Tx) (domain.Store, error) {
	id, err := current(ctx)
	if err != nil {
		return domain.Store{}, err
	}
	row, err := tenancydb.New(tx).GetStore(ctx, id)
	return storeResult(row, err, "get store")
}

// UpdateStore saves the merchant-editable settings.
func UpdateStore(ctx context.Context, tx Tx, s domain.StoreSettings) (domain.Store, error) {
	id, err := current(ctx)
	if err != nil {
		return domain.Store{}, err
	}
	row, err := tenancydb.New(tx).UpdateStore(ctx, tenancydb.UpdateStoreParams{
		StoreID: id, Name: s.Name, Locale: s.Locale, Timezone: s.Timezone,
	})
	return storeResult(row, err, "update store")
}

// SetStoreStatus changes the current store's status.
func SetStoreStatus(ctx context.Context, tx Tx, st domain.StoreStatus) (domain.Store, error) {
	id, err := current(ctx)
	if err != nil {
		return domain.Store{}, err
	}
	row, err := tenancydb.New(tx).SetStoreStatus(ctx, tenancydb.SetStoreStatusParams{StoreID: id, Status: string(st)})
	return storeResult(row, err, "set status")
}

func storeResult(row tenancydb.Store, err error, op string) (domain.Store, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Store{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Store{}, fmt.Errorf("tenancy store: %s: %w", op, err)
	}
	return toStore(row), nil
}

func toStore(r tenancydb.Store) domain.Store {
	return domain.Store{
		ID: tenant.StoreID{ID: r.ID}, Slug: r.Slug, Name: r.Name,
		Status: domain.StoreStatus(r.Status), Plan: domain.Plan(r.Plan),
		Currency: money.Currency(r.Currency), Locale: domain.Locale(r.Locale), Timezone: r.Timezone,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

// NewDomain is a domain row to insert for the current store.
type NewDomain struct {
	ID                ulid.ID
	Host              string
	Kind              domain.HostKind
	Verified          bool // platform subdomains are verified at creation
	Primary           bool
	VerificationToken string
}

// InsertDomain adds a domain to the current store.
func InsertDomain(ctx context.Context, tx Tx, d NewDomain) (domain.Domain, error) {
	id, err := current(ctx)
	if err != nil {
		return domain.Domain{}, err
	}
	p := tenancydb.InsertDomainParams{
		ID: d.ID, StoreID: id, Host: d.Host, Kind: string(d.Kind), Status: string(domain.DomainPending),
		IsPrimary: d.Primary, VerificationToken: d.VerificationToken,
	}
	if d.Verified {
		p.Status = string(domain.DomainVerified)
	}
	row, err := tenancydb.New(tx).InsertDomain(ctx, p)
	if err != nil {
		switch {
		case postgres.IsUniqueViolation(err, "store_domains_store_host_key"):
			return domain.Domain{}, errs.Conflict("domain_exists", "this domain is already added to the store")
		case postgres.IsUniqueViolation(err, "store_domains_verified_host_key"):
			return domain.Domain{}, errs.Conflict("domain_taken", "this domain is already in use")
		}
		return domain.Domain{}, fmt.Errorf("tenancy store: insert domain: %w", err)
	}
	return toDomain(row), nil
}

// ListDomains returns the current store's domains, oldest first.
func ListDomains(ctx context.Context, tx Tx) ([]domain.Domain, error) {
	id, err := current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tenancydb.New(tx).ListDomains(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("tenancy store: list domains: %w", err)
	}
	out := make([]domain.Domain, len(rows))
	for i, r := range rows {
		out[i] = toDomain(r)
	}
	return out, nil
}

// ListHosts returns every host of the current store.
func ListHosts(ctx context.Context, tx Tx) ([]string, error) {
	id, err := current(ctx)
	if err != nil {
		return nil, err
	}
	hosts, err := tenancydb.New(tx).ListStoreHosts(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("tenancy store: list hosts: %w", err)
	}
	return hosts, nil
}

// GetDomain returns one domain of the current store.
func GetDomain(ctx context.Context, tx Tx, domainID ulid.ID) (domain.Domain, error) {
	id, err := current(ctx)
	if err != nil {
		return domain.Domain{}, err
	}
	row, err := tenancydb.New(tx).GetDomain(ctx, tenancydb.GetDomainParams{StoreID: id, ID: domainID})
	return domainResult(row, err, "get domain")
}

// CountCustomDomains counts the current store's custom domains.
func CountCustomDomains(ctx context.Context, tx Tx) (int, error) {
	id, err := current(ctx)
	if err != nil {
		return 0, err
	}
	n, err := tenancydb.New(tx).CountCustomDomains(ctx, id)
	if err != nil {
		return 0, fmt.Errorf("tenancy store: count domains: %w", err)
	}
	return int(n), nil
}

// DeleteCustomDomain removes a non-primary custom domain. It reports
// whether a row was deleted.
func DeleteCustomDomain(ctx context.Context, tx Tx, domainID ulid.ID) (bool, error) {
	id, err := current(ctx)
	if err != nil {
		return false, err
	}
	n, err := tenancydb.New(tx).DeleteCustomDomain(ctx, tenancydb.DeleteCustomDomainParams{StoreID: id, ID: domainID})
	if err != nil {
		return false, fmt.Errorf("tenancy store: delete domain: %w", err)
	}
	return n == 1, nil
}

// SetPrimaryDomain makes a verified domain the primary one.
func SetPrimaryDomain(ctx context.Context, tx Tx, domainID ulid.ID) (domain.Domain, error) {
	id, err := current(ctx)
	if err != nil {
		return domain.Domain{}, err
	}
	q := tenancydb.New(tx)
	if err = q.ClearPrimaryDomain(ctx, id); err != nil {
		return domain.Domain{}, fmt.Errorf("tenancy store: clear primary: %w", err)
	}
	row, err := q.SetPrimaryDomain(ctx, tenancydb.SetPrimaryDomainParams{StoreID: id, ID: domainID})
	return domainResult(row, err, "set primary")
}

// MarkVerified moves a pending domain to verified. A host already verified
// by another store is ErrDomainTaken.
func MarkVerified(ctx context.Context, tx Tx, domainID ulid.ID) (domain.Domain, error) {
	id, err := current(ctx)
	if err != nil {
		return domain.Domain{}, err
	}
	row, err := tenancydb.New(tx).MarkDomainVerified(ctx, tenancydb.MarkDomainVerifiedParams{StoreID: id, ID: domainID})
	if postgres.IsUniqueViolation(err, "store_domains_verified_host_key") {
		return domain.Domain{}, ErrDomainTaken
	}
	return domainResult(row, err, "mark verified")
}

// ErrDomainTaken means another store verified the host first.
var ErrDomainTaken = errors.New("tenancy store: domain verified by another store")

// MarkCheckFailed records a failed check of a pending domain.
func MarkCheckFailed(ctx context.Context, tx Tx, domainID ulid.ID, reason string) (domain.Domain, error) {
	id, err := current(ctx)
	if err != nil {
		return domain.Domain{}, err
	}
	row, err := tenancydb.New(tx).MarkDomainCheckFailed(ctx, tenancydb.MarkDomainCheckFailedParams{StoreID: id, ID: domainID, FailureReason: reason})
	return domainResult(row, err, "mark check failed")
}

// MarkFailed gives up on a pending domain.
func MarkFailed(ctx context.Context, tx Tx, domainID ulid.ID, reason string) (domain.Domain, error) {
	id, err := current(ctx)
	if err != nil {
		return domain.Domain{}, err
	}
	row, err := tenancydb.New(tx).MarkDomainFailed(ctx, tenancydb.MarkDomainFailedParams{StoreID: id, ID: domainID, FailureReason: reason})
	return domainResult(row, err, "mark failed")
}

// ResetPending restarts verification of a pending or failed domain.
func ResetPending(ctx context.Context, tx Tx, domainID ulid.ID) (domain.Domain, error) {
	id, err := current(ctx)
	if err != nil {
		return domain.Domain{}, err
	}
	row, err := tenancydb.New(tx).ResetDomainPending(ctx, tenancydb.ResetDomainPendingParams{StoreID: id, ID: domainID})
	return domainResult(row, err, "reset pending")
}

func domainResult(row tenancydb.StoreDomain, err error, op string) (domain.Domain, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Domain{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Domain{}, fmt.Errorf("tenancy store: %s: %w", op, err)
	}
	return toDomain(row), nil
}

func toDomain(r tenancydb.StoreDomain) domain.Domain {
	return domain.Domain{
		ID: r.ID, StoreID: tenant.StoreID{ID: r.StoreID}, Host: r.Host,
		Kind: domain.HostKind(r.Kind), Status: domain.HostStatus(r.Status), IsPrimary: r.IsPrimary,
		VerificationToken: r.VerificationToken, FailureReason: r.FailureReason, CheckAttempts: int(r.CheckAttempts),
		VerifiedAt: r.VerifiedAt, LastCheckedAt: r.LastCheckedAt, CreatedAt: r.CreatedAt,
	}
}

// ResolveHost looks up a verified host across all stores through the
// SECURITY DEFINER function. found is false for unknown hosts.
func ResolveHost(ctx context.Context, db Tx, host string) (res domain.Resolution, found bool, err error) {
	row, err := tenancydb.New(db).ResolveHost(ctx, host)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Resolution{}, false, nil
	}
	if err != nil {
		return domain.Resolution{}, false, fmt.Errorf("tenancy store: resolve host: %w", err)
	}
	return domain.Resolution{StoreID: tenant.StoreID{ID: row.StoreID}, Status: domain.StoreStatus(row.StoreStatus)}, true, nil
}
