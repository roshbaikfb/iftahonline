-- River job queue schema, River migration line "main" version 003 (ADR-0003).
--
-- Generated with (do not edit by hand):
--   go run github.com/riverqueue/river/cmd/river@v0.49.0 migrate-get --line main --version 3 --up
--   go run github.com/riverqueue/river/cmd/river@v0.49.0 migrate-get --line main --version 3 --down
-- One goose migration per River version, because River runs each version in
-- its own transaction (e.g. 004 adds an enum value that later DDL uses).

-- +goose Up
-- +goose StatementBegin
ALTER TABLE river_job ALTER COLUMN tags SET DEFAULT '{}';
UPDATE river_job SET tags = '{}' WHERE tags IS NULL;
ALTER TABLE river_job ALTER COLUMN tags SET NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE river_job
    ALTER COLUMN tags DROP NOT NULL,
    ALTER COLUMN tags DROP DEFAULT;
-- +goose StatementEnd
