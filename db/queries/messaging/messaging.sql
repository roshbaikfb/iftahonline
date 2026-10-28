-- name: InsertMessage :exec
INSERT INTO messages (store_id, created_at, id, kind, locale, order_id, status, error_code, to_masked, content, quota_period)
VALUES (@store_id, @created_at, @id, @kind, @locale, sqlc.narg(order_id), @status, @error_code, @to_masked,
        sqlc.narg(content), sqlc.narg(quota_period));

-- name: GetMessage :one
-- created_at is the ID's time: one partition.
SELECT * FROM messages
WHERE store_id = @store_id AND created_at = @created_at AND id = @id;

-- name: LockMessage :one
SELECT * FROM messages
WHERE store_id = @store_id AND created_at = @created_at AND id = @id
FOR UPDATE;

-- name: UpdateMessage :exec
UPDATE messages
SET status = @status, channel = @channel, provider = @provider, provider_message_id = @provider_message_id,
    fallback = @fallback, error_code = @error_code, updated_at = now()
WHERE store_id = @store_id AND created_at = @created_at AND id = @id;

-- name: RecordReply :one
-- The first answer wins.
UPDATE messages
SET reply = @reply, replied_at = @replied_at, updated_at = now()
WHERE store_id = @store_id AND created_at = @created_at AND id = @id AND reply = ''
RETURNING id;

-- name: ListMessages :many
-- The log, newest first, after a keyset cursor; status '' is any.
SELECT * FROM messages
WHERE store_id = @store_id AND (created_at, id) < (@before_at::timestamptz, @before_id::uuid)
  AND (@status::text = '' OR status = @status::text)
ORDER BY created_at DESC, id DESC
LIMIT @lim;

-- name: ListOrderMessages :many
-- An order's messages, newest first (an order has a few).
SELECT * FROM messages
WHERE store_id = @store_id AND order_id = @order_id
ORDER BY created_at DESC, id DESC
LIMIT 50;

-- name: ListTemplates :many
SELECT * FROM message_templates WHERE store_id = @store_id ORDER BY kind, locale;

-- name: GetTemplate :one
SELECT sms_body FROM message_templates WHERE store_id = @store_id AND kind = @kind AND locale = @locale;

-- name: SaveTemplate :exec
INSERT INTO message_templates (store_id, kind, locale, sms_body)
VALUES (@store_id, @kind, @locale, @sms_body)
ON CONFLICT (store_id, kind, locale) DO UPDATE SET sms_body = excluded.sms_body, updated_at = now();

-- name: DeleteTemplate :execrows
DELETE FROM message_templates WHERE store_id = @store_id AND kind = @kind AND locale = @locale;

-- name: GetSettings :one
SELECT * FROM messaging_settings WHERE store_id = @store_id;

-- name: SaveSettings :one
INSERT INTO messaging_settings (store_id, confirm_cod, auto_cancel_hours)
VALUES (@store_id, @confirm_cod, @auto_cancel_hours)
ON CONFLICT (store_id) DO UPDATE SET confirm_cod = excluded.confirm_cod, auto_cancel_hours = excluded.auto_cancel_hours, updated_at = now()
RETURNING *;

-- name: InsertConfirmation :execrows
-- A redelivered order.placed finds the row and does nothing.
INSERT INTO order_confirmations (store_id, order_id, status, token_hash, expires_at)
VALUES (@store_id, @order_id, @status, sqlc.narg(token_hash), @expires_at)
ON CONFLICT (store_id, order_id) DO NOTHING;

-- name: LockConfirmation :one
SELECT * FROM order_confirmations WHERE store_id = @store_id AND order_id = @order_id FOR UPDATE;

-- name: GetConfirmation :one
SELECT * FROM order_confirmations WHERE store_id = @store_id AND order_id = @order_id;

-- name: UpdateConfirmation :exec
UPDATE order_confirmations
SET message_id = sqlc.narg(message_id), status = @status, answered_at = sqlc.narg(answered_at), answered_by = @answered_by,
    updated_at = now()
WHERE store_id = @store_id AND order_id = @order_id;

-- name: InsertNotification :execrows
-- Once per (kind, dedup_key).
INSERT INTO merchant_notifications (store_id, id, kind, dedup_key, data)
VALUES (@store_id, @id, @kind, @dedup_key, @data)
ON CONFLICT ON CONSTRAINT merchant_notifications_once DO NOTHING;

-- name: GetNotification :one
SELECT * FROM merchant_notifications WHERE store_id = @store_id AND id = @id;

-- name: ListNotifications :many
-- Newest first (IDs are ULIDs), after a cursor; unread_only filters.
SELECT * FROM merchant_notifications
WHERE store_id = @store_id AND id < @before_id AND (NOT @unread_only::boolean OR read_at IS NULL)
ORDER BY id DESC
LIMIT @lim;

-- name: CountUnreadNotifications :one
SELECT count(*) FROM merchant_notifications WHERE store_id = @store_id AND read_at IS NULL;

-- name: MarkNotificationsRead :execrows
UPDATE merchant_notifications SET read_at = now()
WHERE store_id = @store_id AND id = ANY(@ids::uuid[]) AND read_at IS NULL;

-- name: PurgeNotifications :exec
-- A few of the store's notices older than 90 days, as new ones arrive.
DELETE FROM merchant_notifications m
WHERE m.store_id = @store_id AND m.id IN (
  SELECT n.id FROM merchant_notifications n WHERE n.store_id = @store_id AND n.created_at < @before ORDER BY n.id LIMIT 100
);
