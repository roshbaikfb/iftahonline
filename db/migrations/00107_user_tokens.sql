-- One-time links sent by email (D3, the approved mockups): resetting a
-- forgotten password (an hour) and confirming an email address (two
-- days). Only SHA-256 of the link's secret is stored; the link itself
-- waits sealed in Redis until its email job sends it, as invitations do.
-- A link is used once. Platform-wide like users: no store, no tenant RLS.
--
-- Expand only: a new table.

-- +goose Up
CREATE TABLE user_tokens (
  id         uuid        PRIMARY KEY,
  user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  purpose    text        NOT NULL CHECK (purpose IN ('password_reset', 'email_verify')),
  token_hash bytea       NOT NULL CHECK (octet_length(token_hash) = 32),
  email      citext      NOT NULL, -- the address it was sent to (a verification confirms that one)
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  used_at    timestamptz,
  CONSTRAINT user_tokens_token_hash_key UNIQUE (token_hash)
);
CREATE INDEX user_tokens_user_id_idx ON user_tokens (user_id, purpose);

-- +goose Down
DROP TABLE IF EXISTS user_tokens;
