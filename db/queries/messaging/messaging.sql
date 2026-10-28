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
