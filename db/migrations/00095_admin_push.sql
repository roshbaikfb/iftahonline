-- The console's mobile notifications (ADR-0080): each browser or phone
-- an admin turned them on in is a Web Push subscription. The endpoint is
-- the push service's URL for that device; p256dh and auth encrypt what
-- is sent to it (RFC 8291).

-- +goose Up
CREATE TABLE admin_push_subscriptions (
  id           uuid        PRIMARY KEY,
  admin_id     uuid        NOT NULL REFERENCES platform_admins (id) ON DELETE CASCADE,
  endpoint     text        NOT NULL CHECK (char_length(endpoint) BETWEEN 10 AND 2048 AND endpoint LIKE 'https://%'),
  p256dh       text        NOT NULL CHECK (char_length(p256dh) BETWEEN 80 AND 100),
  auth         text        NOT NULL CHECK (char_length(auth) BETWEEN 16 AND 32),
  user_agent   text        NOT NULL DEFAULT '' CHECK (char_length(user_agent) <= 200),
  created_at   timestamptz NOT NULL DEFAULT now(),
  last_sent_at timestamptz,
  CONSTRAINT admin_push_subscriptions_endpoint_key UNIQUE (endpoint)
);
CREATE INDEX admin_push_subscriptions_admin_idx ON admin_push_subscriptions (admin_id);

REVOKE ALL ON admin_push_subscriptions FROM app_user;
GRANT SELECT, INSERT, UPDATE, DELETE ON admin_push_subscriptions TO app_admin;

-- +goose Down
DROP TABLE IF EXISTS admin_push_subscriptions;
