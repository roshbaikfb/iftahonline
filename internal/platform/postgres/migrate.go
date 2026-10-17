package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx" for goose
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/roshbaikfb/iftahonline/db/migrations"
)

// MigrateCommand is a `platform migrate` sub-command.
type MigrateCommand string

// Migration commands.
const (
	MigrateUp     MigrateCommand = "up"
	MigrateDown   MigrateCommand = "down" // one step
	MigrateStatus MigrateCommand = "status"
)

// Migrate applies the embedded goose migrations. It takes a Postgres
// session advisory lock so concurrent deploys cannot race.
func Migrate(ctx context.Context, dbURL string, cmd MigrateCommand, log *slog.Logger, out io.Writer) (err error) {
	db, err := sql.Open("pgx", dbURL)
	if err != nil {
		return fmt.Errorf("migrate: open: %w", err)
	}
	defer func() { err = errors.Join(err, db.Close()) }()

	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("migrate: locker: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS, goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("migrate: provider: %w", err)
	}

	switch cmd {
	case MigrateUp:
		res, err := p.Up(ctx)
		for _, r := range res {
			log.InfoContext(ctx, "migration applied", "version", r.Source.Version, "file", r.Source.Path, "duration", r.Duration)
		}
		if err != nil {
			return fmt.Errorf("migrate up: %w", err)
		}
	case MigrateDown:
		r, err := p.Down(ctx)
		if err != nil {
			return fmt.Errorf("migrate down: %w", err)
		}
		log.InfoContext(ctx, "migration rolled back", "version", r.Source.Version, "file", r.Source.Path)
	case MigrateStatus:
		st, err := p.Status(ctx)
		if err != nil {
			return fmt.Errorf("migrate status: %w", err)
		}
		for _, s := range st {
			if _, err := fmt.Fprintf(out, "%-8s %5d  %s\n", s.State, s.Source.Version, s.Source.Path); err != nil {
				return fmt.Errorf("migrate status: %w", err)
			}
		}
	default:
		return fmt.Errorf("migrate: unknown command %q (want up, down or status)", cmd)
	}
	return nil
}
