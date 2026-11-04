-- The console's team page (ADR-0078): an owner invites an admin, or
-- resets one who lost the authenticator, with a one-time link. The
-- invitee sets the password and scans the TOTP seed kept here, sealed for
-- the admin it creates or resets; the owner never sees either.

-- +goose Up
CREATE TABLE admin_invites (
  id           uuid        PRIMARY KEY,
  token_hash   bytea       NOT NULL CHECK (octet_length(token_hash) = 32),
  purpose      text        NOT NULL CHECK (purpose IN ('join', 'reset')),
  admin_id     uuid        NOT NULL, -- the admin it creates (join) or resets
  email        citext      NOT NULL CHECK (char_length(email) BETWEEN 3 AND 254),
  name         text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
  role         text        NOT NULL CHECK (role IN ('owner', 'operations', 'finance', 'content', 'viewer')),
  totp_secret  bytea       NOT NULL, -- sealed (AAD "admin-totp:<admin_id>")
  invited_by   uuid        NOT NULL REFERENCES platform_admins (id),
  created_at   timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL,
  used_at      timestamptz,
  cancelled_at timestamptz,
  CONSTRAINT admin_invites_token_hash_key UNIQUE (token_hash)
);
-- One open invite per admin, and per email for a new one.
CREATE UNIQUE INDEX admin_invites_open_admin ON admin_invites (admin_id) WHERE used_at IS NULL AND cancelled_at IS NULL;
CREATE UNIQUE INDEX admin_invites_open_email ON admin_invites (email) WHERE used_at IS NULL AND cancelled_at IS NULL AND purpose = 'join';
CREATE INDEX admin_invites_invited_by_idx ON admin_invites (invited_by);

REVOKE ALL ON admin_invites FROM app_user;
GRANT SELECT, INSERT, UPDATE ON admin_invites TO app_admin;

-- +goose Down
DROP TABLE IF EXISTS admin_invites;
