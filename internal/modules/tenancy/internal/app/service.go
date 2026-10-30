package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/roshbaikfb/iftahonline/internal/modules/entitlements"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/modules/tenancy/internal/store"
	"github.com/roshbaikfb/iftahonline/internal/platform/audit"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/jobs"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// SecondInvalidationDelay schedules a second cache invalidation after a
// change, so a lookup that read the old row just before commit and cached
// it just after the first invalidation cannot keep serving it.
const SecondInvalidationDelay = 10 * time.Second

// Service implements the tenancy use cases. Every method acts on the store
// in ctx (tenant.FromContext).
type Service struct {
	db         postgres.Beginner
	jobs       jobs.Inserter
	ids        *ulid.Generator
	baseDomain string
	audit      audit.Recorder
	plan       entitlements.Entitlements
}

// NewService builds the service. baseDomain is the platform domain under
// which store subdomains live (e.g. iftahonline.com); rec records store
// actions in the audit log, in the same transaction as the action; plan
// admits custom domains within the store's plan (nil admits every one).
func NewService(db postgres.Beginner, ins jobs.Inserter, ids *ulid.Generator, baseDomain string, rec audit.Recorder,
	plan entitlements.Entitlements,
) *Service {
	if plan == nil {
		plan = entitlements.Unrestricted()
	}
	return &Service{db: db, jobs: ins, ids: ids, baseDomain: baseDomain, audit: rec, plan: plan}
}

var errStoreNotFound = errs.NotFound("store_not_found", "store not found")

func notFound(err error, what error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return what
	}
	return err
}

// CreateStoreTx creates the store whose ID is the tenant in ctx, with its
// verified primary subdomain, inside the caller's tenant transaction
// (ADR-0007: identity's sign-up owns the transaction).
func (s *Service) CreateStoreTx(ctx context.Context, tx pgx.Tx, in domain.NewStore) (domain.Store, error) {
	in, err := domain.ValidateNewStore(in)
	if err != nil {
		return domain.Store{}, err
	}
	st, err := store.InsertStore(ctx, tx, in)
	if err != nil {
		return domain.Store{}, err
	}
	host := domain.SubdomainHost(st.Slug, s.baseDomain)
	if _, err = store.InsertDomain(ctx, tx, store.NewDomain{
		ID: s.ids.New(), Host: host, Kind: domain.KindSubdomain, Verified: true, Primary: true,
	}); err != nil {
		return domain.Store{}, err
	}
	// The host may sit in a negative cache from before it existed.
	if err = s.invalidate(ctx, tx, host); err != nil {
		return domain.Store{}, err
	}
	return st, nil
}

// GetStore returns the current store.
func (s *Service) GetStore(ctx context.Context) (domain.Store, error) {
	var st domain.Store
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		st, err = store.GetStore(ctx, tx)
		return err
	})
	return st, notFound(err, errStoreNotFound)
}

// UpdateSettings changes the merchant-editable settings.
func (s *Service) UpdateSettings(ctx context.Context, in domain.StoreSettings) (domain.Store, error) {
	in, err := domain.ValidateSettings(in)
	if err != nil {
		return domain.Store{}, err
	}
	var st domain.Store
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		if st, err = store.UpdateStore(ctx, tx, in); err != nil {
			return err
		}
		return s.audit.RecordTx(ctx, tx, audit.Entry{Action: "store.updated", TargetType: "store", TargetID: st.ID.String(),
			Metadata: map[string]any{"name": st.Name, "locale": string(st.Locale), "timezone": st.Timezone}})
	})
	return st, notFound(err, errStoreNotFound)
}

// SetStatus changes the current store's status and invalidates every one
// of its hosts, so storefronts stop (or resume) serving within seconds.
func (s *Service) SetStatus(ctx context.Context, status domain.StoreStatus) (domain.Store, error) {
	if _, ok := domain.ParseStoreStatus(string(status)); !ok {
		return domain.Store{}, errs.Validation(errs.FieldError{Field: "status", Code: "invalid", Message: "unknown store status"})
	}
	var st domain.Store
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		if st, err = store.SetStoreStatus(ctx, tx, status); err != nil {
			return err
		}
		hosts, err := store.ListHosts(ctx, tx)
		if err != nil {
			return err
		}
		if err := s.audit.RecordTx(ctx, tx, audit.Entry{Action: "store.status_changed", TargetType: "store", TargetID: st.ID.String(),
			Metadata: map[string]any{"status": string(status)}}); err != nil {
			return err
		}
		return s.invalidate(ctx, tx, hosts...)
	})
	return st, notFound(err, errStoreNotFound)
}

// ListDomains returns the current store's domains.
func (s *Service) ListDomains(ctx context.Context) ([]domain.Domain, error) {
	var out []domain.Domain
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		out, err = store.ListDomains(ctx, tx)
		return err
	})
	return out, err
}

var (
	errDomainNotFound      = errs.NotFound("domain_not_found", "domain not found")
	errDomainLimit         = errs.Conflict("domain_limit", fmt.Sprintf("a store can have at most %d custom domains", domain.MaxCustomDomains))
	errRemoveSubdomain     = errs.Conflict("cannot_remove_subdomain", "the platform subdomain cannot be removed")
	errRemovePrimary       = errs.Conflict("cannot_remove_primary", "make another domain primary first")
	errDomainNotVerified   = errs.Conflict("domain_not_verified", "only verified domains can be primary")
	errDomainAlreadyActive = errs.Conflict("domain_verified", "the domain is already verified")
)

// AddCustomDomain registers a merchant domain as pending and starts
// verification.
func (s *Service) AddCustomDomain(ctx context.Context, rawHost string) (domain.Domain, error) {
	host, err := domain.ValidateCustomDomain(rawHost, s.baseDomain)
	if err != nil {
		return domain.Domain{}, err
	}
	token, err := domain.NewVerificationToken()
	if err != nil {
		return domain.Domain{}, err
	}
	var d domain.Domain
	err = postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		n, txErr := store.CountCustomDomains(ctx, tx)
		if txErr != nil {
			return txErr
		}
		if n >= domain.MaxCustomDomains {
			return errDomainLimit
		}
		// Within the plan's custom domains; a refusal is the merchant's
		// 403 plan_limit.
		if _, txErr = s.plan.AdmitTx(ctx, tx, entitlements.CustomDomains, 1, func(context.Context, pgx.Tx) (int64, error) {
			return int64(n), nil
		}); txErr != nil {
			return txErr
		}
		if d, txErr = store.InsertDomain(ctx, tx, store.NewDomain{
			ID: s.ids.New(), Host: host, Kind: domain.KindCustom, VerificationToken: token,
		}); txErr != nil {
			return txErr
		}
		if txErr = s.auditDomain(ctx, tx, "domain.added", d); txErr != nil {
			return txErr
		}
		return s.enqueueVerify(ctx, tx, d.ID)
	})
	return d, err
}

// VerifyDomain restarts verification of a pending or failed domain now.
func (s *Service) VerifyDomain(ctx context.Context, id ulid.ID) (domain.Domain, error) {
	var d domain.Domain
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		cur, err := store.GetDomain(ctx, tx, id)
		if err != nil {
			return err
		}
		if cur.Status == domain.DomainVerified {
			return errDomainAlreadyActive
		}
		if d, err = store.ResetPending(ctx, tx, id); err != nil {
			return err
		}
		if err = s.auditDomain(ctx, tx, "domain.verification_requested", d); err != nil {
			return err
		}
		return s.enqueueVerify(ctx, tx, id)
	})
	return d, notFound(err, errDomainNotFound)
}

// RemoveDomain deletes a custom, non-primary domain.
func (s *Service) RemoveDomain(ctx context.Context, id ulid.ID) error {
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		d, err := store.GetDomain(ctx, tx, id)
		if err != nil {
			return err
		}
		switch {
		case d.Kind == domain.KindSubdomain:
			return errRemoveSubdomain
		case d.IsPrimary:
			return errRemovePrimary
		}
		deleted, err := store.DeleteCustomDomain(ctx, tx, id)
		if err != nil {
			return err
		}
		if !deleted {
			return domain.ErrNotFound // changed concurrently
		}
		if err := s.auditDomain(ctx, tx, "domain.removed", d); err != nil {
			return err
		}
		if d.Status == domain.DomainVerified {
			return s.invalidate(ctx, tx, d.Host)
		}
		return nil
	})
	return notFound(err, errDomainNotFound)
}

// SetPrimaryDomain makes a verified domain the store's canonical host.
func (s *Service) SetPrimaryDomain(ctx context.Context, id ulid.ID) (domain.Domain, error) {
	var d domain.Domain
	err := postgres.InTenantTx(ctx, s.db, func(tx pgx.Tx) error {
		cur, err := store.GetDomain(ctx, tx, id)
		if err != nil {
			return err
		}
		if cur.Status != domain.DomainVerified {
			return errDomainNotVerified
		}
		if d, err = store.SetPrimaryDomain(ctx, tx, id); err != nil {
			return err
		}
		return s.auditDomain(ctx, tx, "domain.primary_set", d)
	})
	return d, notFound(err, errDomainNotFound)
}

func (s *Service) auditDomain(ctx context.Context, tx pgx.Tx, action string, d domain.Domain) error {
	return s.audit.RecordTx(ctx, tx, audit.Entry{Action: action, TargetType: "domain", TargetID: d.ID.String(),
		Metadata: map[string]any{"host": d.Host}})
}

func (s *Service) enqueueVerify(ctx context.Context, tx pgx.Tx, id ulid.ID) error {
	t, err := jobs.ForTenant(ctx)
	if err != nil {
		return err
	}
	_, err = s.jobs.InsertTx(ctx, tx, VerifyDomainArgs{TenantArgs: t, DomainID: id}, nil)
	if err != nil {
		return fmt.Errorf("tenancy: enqueue verify: %w", err)
	}
	return nil
}

// invalidate enqueues two cache invalidations for hosts in tx: now, and
// SecondInvalidationDelay later.
func (s *Service) invalidate(ctx context.Context, tx pgx.Tx, hosts ...string) error {
	if len(hosts) == 0 {
		return nil
	}
	t, err := jobs.ForTenant(ctx)
	if err != nil {
		return err
	}
	args := InvalidateHostsArgs{TenantArgs: t, Hosts: hosts}
	if _, err = s.jobs.InsertTx(ctx, tx, args, nil); err != nil {
		return fmt.Errorf("tenancy: enqueue invalidation: %w", err)
	}
	later := &river.InsertOpts{ScheduledAt: time.Now().Add(SecondInvalidationDelay)}
	if _, err = s.jobs.InsertTx(ctx, tx, args, later); err != nil {
		return fmt.Errorf("tenancy: enqueue invalidation: %w", err)
	}
	return nil
}

// SlugAvailability says whether a new store could take slug. It needs no
// tenant: sign-up asks before the store exists. It reads the hosts
// directly, not through the resolver's caches, so the answer is current.
func (s *Service) SlugAvailability(ctx context.Context, raw string) (domain.SlugAvailability, error) {
	slug, reason := domain.CheckSlug(raw)
	if reason != "" {
		return domain.SlugAvailability{Slug: slug, Reason: reason}, nil
	}
	var found bool
	err := postgres.InTx(ctx, s.db, func(tx pgx.Tx) (txErr error) {
		_, found, txErr = store.ResolveHost(ctx, tx, domain.SubdomainHost(slug, s.baseDomain))
		return txErr
	})
	if err != nil {
		return domain.SlugAvailability{}, err
	}
	if found {
		return domain.SlugAvailability{Slug: slug, Reason: "taken"}, nil
	}
	return domain.SlugAvailability{Slug: slug, Available: true}, nil
}
