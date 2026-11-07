-- Platform support's delayed requests on a store's team (C1, the approved
-- mockups): removing a member's two-step verification after they lost
-- their phone, and (C2) moving a store's ownership. An admin asks; the
-- change waits 24 hours, and the emails sent at once carry a link that
-- cancels it, so a request made by someone impersonating a merchant is
-- stopped by the real one. Tenant-owned: store_id, RLS.
--
-- The cancel links are user_tokens of purpose 'support_cancel' that name
-- their request and store (link_store_id) (the link page opens without a session, so the
-- token, platform-wide like users, says which store to read).
--
-- app.member_stores lists the other stores a user works on, for the
-- console's warning before it disables an account (it is platform-wide).
--
-- Expand only: a new table, nullable columns, a widened check.
-- 00109 validates the user_tokens constraints.

-- +goose Up
CREATE TABLE support_requests (
  id           uuid        PRIMARY KEY,
  store_id     uuid        NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
  kind         text        NOT NULL CHECK (kind IN ('mfa_reset', 'ownership_transfer')),
  user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE, -- the member (mfa_reset) or the new owner
  from_user_id uuid        REFERENCES users (id) ON DELETE CASCADE,          -- ownership_transfer: the owner now
  requested_by uuid        NOT NULL, -- a platform admin (their own table, another role)
  reason       text        NOT NULL CHECK (char_length(reason) BETWEEN 1 AND 500),
  created_at   timestamptz NOT NULL DEFAULT now(),
  effective_at timestamptz NOT NULL,
  cancelled_at timestamptz,
  cancelled_by text        CHECK (cancelled_by IN ('link', 'console')),
  done_at      timestamptz,
  CONSTRAINT support_requests_closed_once CHECK (cancelled_at IS NULL OR done_at IS NULL),
  CONSTRAINT support_requests_cancel_by CHECK ((cancelled_at IS NULL) = (cancelled_by IS NULL)),
  CONSTRAINT support_requests_transfer_from CHECK ((kind = 'ownership_transfer') = (from_user_id IS NOT NULL))
);
CREATE INDEX support_requests_store_idx ON support_requests (store_id, created_at DESC);
CREATE INDEX support_requests_user_idx ON support_requests (user_id);
CREATE INDEX support_requests_from_user_idx ON support_requests (from_user_id) WHERE from_user_id IS NOT NULL;
-- One open request of a kind per member (and one open transfer per store).
CREATE UNIQUE INDEX support_requests_one_open ON support_requests (store_id, kind, user_id)
  WHERE cancelled_at IS NULL AND done_at IS NULL;
CREATE UNIQUE INDEX support_requests_one_transfer ON support_requests (store_id)
  WHERE kind = 'ownership_transfer' AND cancelled_at IS NULL AND done_at IS NULL;

ALTER TABLE support_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE support_requests FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON support_requests
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());

-- The new constraints are NOT VALID: they bind every new row at once and
-- take no long lock; the rows already there (password and email links)
-- satisfy them, and 00109 validates them.
ALTER TABLE user_tokens
  ADD COLUMN request_id uuid,
  ADD COLUMN link_store_id uuid, -- not store_id: user_tokens is platform-wide, no RLS
  DROP CONSTRAINT user_tokens_purpose_check,
  ADD CONSTRAINT user_tokens_purpose_check CHECK (purpose IN ('password_reset', 'email_verify', 'support_cancel')) NOT VALID,
  ADD CONSTRAINT user_tokens_support_ref CHECK ((purpose = 'support_cancel') = (request_id IS NOT NULL AND link_store_id IS NOT NULL)) NOT VALID,
  ADD CONSTRAINT user_tokens_request_id_fkey FOREIGN KEY (request_id) REFERENCES support_requests (id) ON DELETE CASCADE NOT VALID,
  ADD CONSTRAINT user_tokens_link_store_id_fkey FOREIGN KEY (link_store_id) REFERENCES stores (id) ON DELETE CASCADE NOT VALID;
CREATE INDEX user_tokens_request_idx ON user_tokens (request_id) WHERE request_id IS NOT NULL;
CREATE INDEX user_tokens_link_store_idx ON user_tokens (link_store_id) WHERE link_store_id IS NOT NULL;

-- app.member_stores lists the stores (at most 20) user p_user is a member
-- of, with the role: IDs only, for the console.
-- +goose StatementBegin
CREATE FUNCTION app.member_stores(p_user uuid)
  RETURNS TABLE (store_id uuid, role text)
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT m.store_id, m.role
  FROM public.store_memberships m
  WHERE m.user_id = p_user
  ORDER BY m.created_at, m.store_id
  LIMIT 20
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.member_stores(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.member_stores(uuid) TO app_admin;

-- +goose Down
DROP FUNCTION IF EXISTS app.member_stores(uuid);
DELETE FROM user_tokens WHERE purpose = 'support_cancel';
ALTER TABLE user_tokens
  DROP CONSTRAINT IF EXISTS user_tokens_link_store_id_fkey,
  DROP CONSTRAINT IF EXISTS user_tokens_request_id_fkey,
  DROP CONSTRAINT IF EXISTS user_tokens_support_ref,
  DROP CONSTRAINT user_tokens_purpose_check,
  ADD CONSTRAINT user_tokens_purpose_check CHECK (purpose IN ('password_reset', 'email_verify')),
  DROP COLUMN IF EXISTS link_store_id,
  DROP COLUMN IF EXISTS request_id;
DROP TABLE IF EXISTS support_requests;
