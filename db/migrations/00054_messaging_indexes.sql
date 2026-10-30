-- Indexes for the dashboard's message log and bell (P6 review):
-- - one status's messages, newest first (ListMessagesByStatus), without
--   reading the store's other messages;
-- - the store's unread notices (CountUnreadNotifications), counted on the
--   bell.
--
-- Expand only: two indexes. Plain CREATE INDEX, not CONCURRENTLY: both
-- tables are empty (the platform is not live yet), and messages is
-- partitioned, where CONCURRENTLY is not allowed on the parent. A later
-- index on a live, large messages table must be built per partition
-- with CONCURRENTLY and attached.

-- +goose Up
CREATE INDEX IF NOT EXISTS messages_store_status ON messages (store_id, status, created_at, id);
CREATE INDEX IF NOT EXISTS merchant_notifications_unread ON merchant_notifications (store_id) WHERE read_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS merchant_notifications_unread;
DROP INDEX IF EXISTS messages_store_status;
