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
-- The log, newest first, after a keyset cursor (the primary key).
SELECT * FROM messages
WHERE store_id = @store_id AND (created_at, id) < (@before_at::timestamptz, @before_id::uuid)
ORDER BY created_at DESC, id DESC
LIMIT @lim;

-- name: ListMessagesByStatus :many
-- One status's messages, newest first, after a keyset cursor
-- (messages_store_status): a rare status reads its rows only.
SELECT * FROM messages
WHERE store_id = @store_id AND status = @status AND (created_at, id) < (@before_at::timestamptz, @before_id::uuid)
ORDER BY created_at DESC, id DESC
LIMIT @lim;

-- name: ListOrderMessages :many
-- An order's messages, newest first (an order has a few). None is older
-- than the order, so the months before it are never read.
SELECT * FROM messages
WHERE store_id = @store_id AND order_id = @order_id AND created_at >= @since
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
-- A redelivered order.placed finds the row and does nothing. A wa_code
-- taken by another store's order (1 in 10^12) fails the insert with a
-- unique violation; the caller draws another.
INSERT INTO order_confirmations (store_id, order_id, status, token_hash, wa_code, expires_at)
VALUES (@store_id, @order_id, @status, sqlc.narg(token_hash), sqlc.narg(wa_code), @expires_at)
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

-- name: ConfirmationByToken :one
SELECT * FROM order_confirmations WHERE store_id = @store_id AND token_hash = @token_hash;

-- name: ConfirmationStoreByCode :one
-- The store of a WhatsApp code, across stores (SECURITY DEFINER).
SELECT app.confirmation_store(@code::text)::uuid AS store_id;

-- name: ConfirmationByCode :one
SELECT * FROM order_confirmations WHERE store_id = @store_id AND wa_code = @wa_code;
