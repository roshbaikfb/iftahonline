// Package postgres builds the pgx pool and runs tenant transactions with
// the row-level-security second wall (ADR-0002).
package postgres

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/roshbaikfb/iftahonline/internal/platform/config"
	"github.com/roshbaikfb/iftahonline/internal/platform/tenant"
)

// NewPool returns a pool sized from config. It connects lazily, so startup
// is fast and does not fail when the database is briefly unavailable;
// /readyz reports the database state instead.
func NewPool(ctx context.Context, c config.DB, appName string) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(c.URL)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse url: %w", err) // pgx redacts the password
	}
	pc.MaxConns = c.MaxConns
	pc.MinConns = c.MinConns
	pc.MaxConnLifetime = c.MaxConnLifetime
	pc.MaxConnLifetimeJitter = c.MaxConnLifetime / 10
	pc.MaxConnIdleTime = 5 * time.Minute
	pc.HealthCheckPeriod = 30 * time.Second
	pc.ConnConfig.ConnectTimeout = 5 * time.Second
	pc.ConnConfig.RuntimeParams["application_name"] = appName
	pc.ConnConfig.RuntimeParams["statement_timeout"] = strconv.FormatInt(c.StatementTimeout.Milliseconds(), 10)
	pc.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "60000"

	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("postgres: new pool: %w", err)
	}
	return pool, nil
}

// Beginner is satisfied by *pgxpool.Pool and pgx.Tx (for nesting).
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// InTenantTx runs fn in a transaction scoped to the store in ctx. It sets
// app.store_id with SET LOCAL semantics, so RLS policies see the tenant
// and the setting cannot leak to the next user of the pooled connection.
// A context without a tenant fails with tenant.ErrNoTenant before any SQL
// runs. fn's error rolls the transaction back.
func InTenantTx(ctx context.Context, db Beginner, fn func(pgx.Tx) error) error {
	store, err := tenant.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	return inTx(ctx, db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.store_id', $1::uuid::text, true)", store.ID); err != nil {
			return fmt.Errorf("postgres: set tenant: %w", err)
		}
		return fn(tx)
	})
}

// InTx runs fn in a transaction without a tenant. Use it only for tables
// that are not tenant-owned (e.g. the store directory, River jobs).
func InTx(ctx context.Context, db Beginner, fn func(pgx.Tx) error) error {
	return inTx(ctx, db, fn)
}

func inTx(ctx context.Context, db Beginner, fn func(pgx.Tx) error) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin: %w", err)
	}
	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			return fmt.Errorf("%w (rollback: %w)", err, rbErr)
		}
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit: %w", err)
	}
	return nil
}
