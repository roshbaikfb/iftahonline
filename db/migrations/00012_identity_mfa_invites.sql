-- Identity part 2: phone numbers, TOTP 2FA, recovery codes, known devices
-- and store invitations (ADR-0005). Expand-only: new nullable columns and
-- new tables.

-- +goose Up
ALTER TABLE users
  ADD COLUMN phone             text,          -- E.164, set only once verified
  ADD COLUMN phone_verified_at timestamptz,
  ADD COLUMN totp_secret       bytea,         -- sealed with secretbox (AAD = "totp:<user id>")
  ADD COLUMN totp_enabled_at   timestamptz,   -- NULL while enrolment is unconfirmed
  ADD COLUMN totp_last_step    bigint NOT NULL DEFAULT 0, -- replay guard
  ADD CONSTRAINT users_phone_key UNIQUE (phone),
  ADD CONSTRAINT users_phone_e164 CHECK (phone IS NULL OR phone ~ '^\+[1-9][0-9]{6,14}$'),
  ADD CONSTRAINT users_totp_enabled_has_secret CHECK (totp_enabled_at IS NULL OR totp_secret IS NOT NULL);

-- One-time 2FA recovery codes, stored as SHA-256 (they are 80-bit random).
CREATE TABLE user_recovery_codes (
  user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  code_hash  bytea       NOT NULL CHECK (octet_length(code_hash) = 32),
  created_at timestamptz NOT NULL DEFAULT now(),
  used_at    timestamptz,
  PRIMARY KEY (user_id, code_hash)
);

-- Devices a user has signed in from, by the SHA-256 of a long-lived
-- device cookie (web) or app-generated ID (mobile).
CREATE TABLE user_devices (
  user_id       uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  device_hash   bytea       NOT NULL CHECK (octet_length(device_hash) = 32),
  user_agent    text        NOT NULL DEFAULT '' CHECK (char_length(user_agent) <= 200),
  ip_prefix     text        NOT NULL DEFAULT '',
  first_seen_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, device_hash)
);

-- Pending invitations to join a store. The link carries the store ID and a
-- secret; only the secret's SHA-256 is stored.
CREATE TABLE store_invitations (
  id          uuid        PRIMARY KEY,
  store_id    uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  email       citext      NOT NULL CHECK (char_length(email) BETWEEN 3 AND 254),
  role        text        NOT NULL CHECK (role IN ('manager', 'support', 'shipping')),
  token_hash  bytea       NOT NULL CHECK (octet_length(token_hash) = 32),
  invited_by  uuid        NOT NULL REFERENCES users (id),
  created_at  timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL,
  accepted_at timestamptz,
  revoked_at  timestamptz,
  CONSTRAINT store_invitations_token_hash_key UNIQUE (token_hash)
);
CREATE INDEX store_invitations_store_id_idx ON store_invitations (store_id, created_at DESC);
CREATE INDEX store_invitations_invited_by_idx ON store_invitations (invited_by);
-- One open invitation per email per store.
CREATE UNIQUE INDEX store_invitations_one_open ON store_invitations (store_id, email)
  WHERE accepted_at IS NULL AND revoked_at IS NULL;

ALTER TABLE store_invitations ENABLE ROW LEVEL SECURITY;
ALTER TABLE store_invitations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON store_invitations
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());

-- +goose Down
DROP TABLE store_invitations;
DROP TABLE user_devices;
DROP TABLE user_recovery_codes;
ALTER TABLE users
  DROP CONSTRAINT users_totp_enabled_has_secret,
  DROP CONSTRAINT users_phone_e164,
  DROP CONSTRAINT users_phone_key,
  DROP COLUMN totp_last_step,
  DROP COLUMN totp_enabled_at,
  DROP COLUMN totp_secret,
  DROP COLUMN phone_verified_at,
  DROP COLUMN phone;
