package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/modules/admin"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog"
	"github.com/roshbaikfb/iftahonline/internal/modules/identity"
	"github.com/roshbaikfb/iftahonline/internal/modules/orders"
	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
)

// storeProfiler reads the console's store profile from the modules that
// own each part (identity's owner, orders' activity, catalog's
// products), in one tenant transaction on the app pool. The owner's
// contact is returned, never logged.
func storeProfiler(pool *pgxpool.Pool, now func() time.Time) admin.Profiler {
	return func(ctx context.Context) (p admin.StoreProfile, err error) {
		err = postgres.InTenantTx(ctx, pool, func(tx pgx.Tx) error {
			owner, ok, ownerErr := identity.OwnerContactTx(ctx, tx)
			if ownerErr != nil {
				return ownerErr
			}
			if ok {
				p.Owner = &admin.OwnerContact{Name: owner.Name, Email: owner.Email, Phone: owner.Phone, EmailVerified: owner.EmailVerified,
					LastSeenAt: owner.LastSeenAt}
			}
			act, actErr := orders.ActivityTx(ctx, tx, now().Add(-30*24*time.Hour))
			if actErr != nil {
				return actErr
			}
			p.Orders30d, p.LastOrderAt = act.Since, act.LastOrderAt
			var countErr error
			p.Products, countErr = catalog.CountProducts(ctx, tx)
			return countErr
		})
		if err != nil {
			return admin.StoreProfile{}, fmt.Errorf("store profile: %w", err)
		}
		return p, nil
	}
}
