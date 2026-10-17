// Package migrations embeds the goose SQL migrations so the binary's
// `platform migrate` subcommand needs no files on disk.
package migrations

import "embed"

// FS holds every *.sql migration in this directory.
//
//go:embed *.sql
var FS embed.FS //nolint:gochecknoglobals // go:embed requires a package-level variable.
