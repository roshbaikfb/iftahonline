-- River job queue schema, River migration line "main" version 008 (ADR-0003).
--
-- Generated with (do not edit by hand):
--   go run github.com/riverqueue/river/cmd/river@v0.49.0 migrate-get --line main --version 8 --up
--   go run github.com/riverqueue/river/cmd/river@v0.49.0 migrate-get --line main --version 8 --down
-- One goose migration per River version, because River runs each version in
-- its own transaction (e.g. 004 adds an enum value that later DDL uses).

-- +goose Up
-- +goose StatementBegin
-- No-op. PostgreSQL sequences already prevent automatically generated job IDs
-- from being reused.
SELECT 1;

-- Record the applied River versions so River's own migrator and
-- rivermigrate.Validate agree with goose.
INSERT INTO river_migration (line, version)
SELECT 'main', v FROM generate_series(1, 8) AS v;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM river_migration WHERE line = 'main';

-- No-op. PostgreSQL sequences already prevent automatically generated job IDs
-- from being reused.
SELECT 1;
-- +goose StatementEnd
