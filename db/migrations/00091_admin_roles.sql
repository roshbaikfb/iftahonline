-- What each console admin may do (ADR-0077). Every admin until now could
-- do everything, so each existing one becomes an owner.

-- +goose Up
ALTER TABLE platform_admins ADD COLUMN IF NOT EXISTS role text NOT NULL DEFAULT 'owner'
  CHECK (role IN ('owner', 'operations', 'finance', 'content', 'viewer'));

-- +goose Down
ALTER TABLE platform_admins DROP COLUMN IF EXISTS role;
