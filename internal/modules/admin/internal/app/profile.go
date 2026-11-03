package app

import (
	"context"
	"errors"
	"time"

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

// StoreProfile is store id's owner and numbers. An unknown store is 404.
// Nothing here is logged: the owner's contact is for the admin's eyes.
func (s *Service) StoreProfile(ctx context.Context, id tenant.StoreID) (StoreProfile, error) {
	if _, ok := IdentityFrom(ctx); !ok {
		return StoreProfile{}, errNoSession
	}
	if s.d.Profile == nil {
		return StoreProfile{}, errNoProfile
	}
	if _, _, err := s.d.Directory.Store(ctx, id); err != nil {
		return StoreProfile{}, err
	}
	return s.d.Profile(tenant.WithStore(ctx, id))
}
