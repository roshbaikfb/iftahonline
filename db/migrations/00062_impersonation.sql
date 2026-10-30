-- Impersonation by platform admins (ADR-0055).
--
-- An admin opens the dashboard as a store's owner for a while: a session
-- of the owner's user, marked with the admin and scoped to that one
-- store, read-only unless asked, that ends at its expiry (never slid).
-- The console records the start in its own log first; identity records
-- it in the store's log, and every request the session makes.
--
-- Expand only: nullable or defaulted columns and NOT VALID constraints.

-- +goose Up
ALTER TABLE user_sessions ADD COLUMN IF NOT EXISTS impersonator_id uuid;
ALTER TABLE user_sessions ADD COLUMN IF NOT EXISTS scope_store_id uuid;
ALTER TABLE user_sessions ADD COLUMN IF NOT EXISTS read_only boolean NOT NULL DEFAULT false;
ALTER TABLE user_sessions ADD CONSTRAINT user_sessions_scope_store_id_fkey
  FOREIGN KEY (scope_store_id) REFERENCES stores (id) ON DELETE CASCADE NOT VALID;
ALTER TABLE user_sessions ADD CONSTRAINT user_sessions_impersonation_scoped
  CHECK (impersonator_id IS NULL OR scope_store_id IS NOT NULL) NOT VALID;
CREATE INDEX IF NOT EXISTS user_sessions_scope_store_id_idx ON user_sessions (scope_store_id) WHERE scope_store_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS user_sessions_scope_store_id_idx;
ALTER TABLE user_sessions DROP CONSTRAINT IF EXISTS user_sessions_impersonation_scoped;
ALTER TABLE user_sessions DROP CONSTRAINT IF EXISTS user_sessions_scope_store_id_fkey;
ALTER TABLE user_sessions DROP COLUMN IF EXISTS read_only;
ALTER TABLE user_sessions DROP COLUMN IF EXISTS scope_store_id;
ALTER TABLE user_sessions DROP COLUMN IF EXISTS impersonator_id;
