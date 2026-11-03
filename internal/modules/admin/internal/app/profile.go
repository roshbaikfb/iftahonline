package app

import (
	"context"
	"errors"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

// OwnerContact is how to reach a store's owner: shown to admins on the
// store's page, never logged.
type OwnerContact struct {
	Name, Email, Phone string
	EmailVerified      bool
	LastSeenAt         time.Time // zero: never
}

// StoreProfile is a store's owner and numbers (the console's store page).
type StoreProfile struct {
	Owner       *OwnerContact // nil: the store has no owner
	Orders30d   int64
	LastOrderAt time.Time // zero: never
	Products    int64
}

// Profiler reads the profile of the store in ctx (wired in cmd/platform
// from identity, orders and catalog, in one tenant transaction).
type Profiler func(ctx context.Context) (StoreProfile, error)

var errNoProfile = errs.Unavailable(0, errors.New("admin: store profiles are not wired"))

// StoreProfile is store id's owner and numbers, the owner left out for a
// role without contacts.read. An unknown store is 404.
// Nothing here is logged: the owner's contact is for the admin's eyes.
func (s *Service) StoreProfile(ctx context.Context, id tenant.StoreID) (StoreProfile, error) {
	who, ok := IdentityFrom(ctx)
	if !ok {
		return StoreProfile{}, errNoSession
	}
	if s.d.Profile == nil {
		return StoreProfile{}, errNoProfile
	}
	if _, _, err := s.d.Directory.Store(ctx, id); err != nil {
		return StoreProfile{}, err
	}
	p, err := s.d.Profile(tenant.WithStore(ctx, id))
	if err == nil && !who.Can(domain.PermContactsRead) {
		p.Owner = nil // the owner's contact is for owners and operations only (ADR-0077)
	}
	return p, err
}
