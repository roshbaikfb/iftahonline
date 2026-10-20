// Package testinfra starts real PostgreSQL and Redis containers for
// integration tests (CLAUDE.md testing rules). Production code must not
// import it.
//
// Typical use from TestMain:
//
//	func TestMain(m *testing.M) { os.Exit(testinfra.Run(m, &infra)) }
package testinfra

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/roshbaikfb/iftahonline/internal/platform/postgres"
)

// Images are pinned to the versions in deploy/compose/docker-compose.yml.
const (
	PostgresImage = "postgres:16-alpine"
	RedisImage    = "redis:7.4-alpine"
)

// Infra holds connection URLs for the started services.
type Infra struct {
	OwnerURL string // app_owner: migrations and test fixtures
	AppURL   string // app_user: what services use (NOBYPASSRLS)
	AdminURL string // app_admin: the super-admin console's role
	RedisURL string
}

// Run starts PostgreSQL (migrated) and Redis, runs the tests, and tears
// everything down. It returns the exit code for os.Exit. Under -fuzz no
// containers start: fuzz runs use -run='^$', so only the (pure) fuzz
// target executes.
func Run(m *testing.M, infra *Infra) int {
	flag.Parse()
	if f := flag.Lookup("test.fuzz"); f != nil && f.Value.String() != "" {
		return m.Run()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cleanup, err := start(ctx, infra)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "testinfra: %v\n", err)
		if cleanup != nil {
			cleanup()
		}
		return 1
	}
	code := m.Run()
	cleanup()
	return code
}

// Start brings up the same migrated PostgreSQL and Redis for tools that
// are not tests (the storefront demo used by browser tests). Call stop
// when done.
func Start(ctx context.Context) (Infra, func(), error) {
	var infra Infra
	stop, err := start(ctx, &infra)
	if err != nil && stop != nil {
		stop()
	}
	return infra, stop, err
}

func start(ctx context.Context, infra *Infra) (func(), error) {
	var containers []testcontainers.Container
	cleanup := func() {
		for _, c := range containers {
			if err := testcontainers.TerminateContainer(c); err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "testinfra: terminate: %v\n", err)
			}
		}
	}

	pg, err := tcpostgres.Run(ctx, PostgresImage,
		tcpostgres.WithDatabase("postgres"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		tcpostgres.WithInitScripts(filepath.Join(repoRoot(), "deploy", "compose", "initdb", "01-roles.sql")),
		tcpostgres.BasicWaitStrategies(),
	)
	if pg != nil {
		containers = append(containers, pg)
	}
	if err != nil {
		return cleanup, fmt.Errorf("start postgres: %w", err)
	}
	host, err := pg.Host(ctx)
	if err != nil {
		return cleanup, fmt.Errorf("postgres host: %w", err)
	}
	port, err := pg.MappedPort(ctx, "5432/tcp")
	if err != nil {
		return cleanup, fmt.Errorf("postgres port: %w", err)
	}
	dsn := func(user string) string {
		return fmt.Sprintf("postgres://%s:%s@%s:%s/iftah?sslmode=disable", user, user, host, port.Port())
	}
	infra.OwnerURL, infra.AppURL, infra.AdminURL = dsn("app_owner"), dsn("app_user"), dsn("app_admin")

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err = postgres.Migrate(ctx, infra.OwnerURL, postgres.MigrateUp, quiet, io.Discard); err != nil {
		return cleanup, fmt.Errorf("migrate: %w", err)
	}

	rd, err := tcredis.Run(ctx, RedisImage)
	if rd != nil {
		containers = append(containers, rd)
	}
	if err != nil {
		return cleanup, fmt.Errorf("start redis: %w", err)
	}
	if infra.RedisURL, err = rd.ConnectionString(ctx); err != nil {
		return cleanup, fmt.Errorf("redis url: %w", err)
	}
	return cleanup, nil
}

// repoRoot finds the repository root: from this source file's location,
// or, in a binary built with -trimpath (whose paths are module-relative),
// by walking up from the working directory to the directory holding the
// database init scripts (go.mod alone is not enough: web/dashboard has
// one to keep itself out of the Go module).
func repoRoot() string {
	if _, file, _, ok := runtime.Caller(0); ok && filepath.IsAbs(file) {
		return filepath.Join(filepath.Dir(file), "..", "..", "..")
	}
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err = os.Stat(filepath.Join(dir, "deploy", "compose", "initdb", "01-roles.sql")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}
