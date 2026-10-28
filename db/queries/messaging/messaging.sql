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
