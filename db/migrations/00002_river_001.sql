-- River job queue schema, River migration line "main" version 001 (ADR-0003).
--
-- Generated with (do not edit by hand):
--   go run github.com/riverqueue/river/cmd/river@v0.49.0 migrate-get --line main --version 1 --up
--   go run github.com/riverqueue/river/cmd/river@v0.49.0 migrate-get --line main --version 1 --down
-- One goose migration per River version, because River runs each version in
-- its own transaction (e.g. 004 adds an enum value that later DDL uses).

-- +goose Up
-- +goose StatementBegin
CREATE TABLE river_migration(
  id bigserial PRIMARY KEY,
  created_at timestamptz NOT NULL DEFAULT NOW(),
  version bigint NOT NULL,
  CONSTRAINT version CHECK (version >= 1)
);

CREATE UNIQUE INDEX ON river_migration USING btree(version);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE river_migration;
-- +goose StatementEnd
