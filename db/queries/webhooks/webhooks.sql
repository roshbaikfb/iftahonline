-- name: InsertReceipt :execrows
-- The once-only gate: 0 rows means the provider event was already
-- accepted (a replay).
INSERT INTO webhook_receipts (id, store_id, provider, connection_id, event_id, received_at)
VALUES (@id, @store_id, @provider, @connection_id, @event_id, @received_at)
ON CONFLICT (store_id, provider, event_id) DO NOTHING;

-- name: InsertBody :exec
INSERT INTO webhook_bodies (store_id, received_at, receipt_id, content_type, body)
VALUES (@store_id, @received_at, @receipt_id, @content_type, @body);

-- name: GetReceipt :one
SELECT * FROM webhook_receipts WHERE store_id = @store_id AND id = @id;

-- name: GetBody :one
SELECT content_type, body FROM webhook_bodies
WHERE store_id = @store_id AND received_at = @received_at AND receipt_id = @receipt_id;

-- name: MarkProcessed :execrows
-- First outcome wins: a job that runs again does not overwrite it.
UPDATE webhook_receipts SET processed_at = now(), outcome = @outcome
WHERE store_id = @store_id AND id = @id AND processed_at IS NULL;

-- name: PurgeReceipts :exec
-- A few of the store's expired receipts per accepted webhook.
DELETE FROM webhook_receipts AS d
WHERE d.store_id = @store_id AND d.id IN (
  SELECT r.id FROM webhook_receipts r
  WHERE r.store_id = @store_id AND r.received_at < @before
  ORDER BY r.received_at LIMIT 20);
