-- name: GetDraft :one
SELECT * FROM storefront_drafts WHERE store_id = @store_id;

-- name: SaveThemeDraft :one
INSERT INTO storefront_drafts (store_id, theme) VALUES (@store_id, @theme)
ON CONFLICT (store_id) DO UPDATE SET theme = excluded.theme, updated_at = now()
RETURNING *;

-- name: SaveHomeDraft :one
INSERT INTO storefront_drafts (store_id, home) VALUES (@store_id, @home)
ON CONFLICT (store_id) DO UPDATE SET home = excluded.home, updated_at = now()
RETURNING *;

-- name: ClearThemeDraft :exec
UPDATE storefront_drafts SET theme = NULL, updated_at = now() WHERE store_id = @store_id;

-- name: ClearHomeDraft :exec
UPDATE storefront_drafts SET home = NULL, updated_at = now() WHERE store_id = @store_id;

-- name: DeleteExpiredPreviews :exec
-- A store's expired links go when it makes a new one
-- (storefront_previews_store).
DELETE FROM storefront_previews WHERE store_id = @store_id AND expires_at < now();

-- name: CountPreviews :one
SELECT count(*) FROM storefront_previews WHERE store_id = @store_id;

-- name: InsertPreview :exec
INSERT INTO storefront_previews (token_hash, store_id, expires_at) VALUES (@token_hash, @store_id, @expires_at);

-- name: PreviewDraft :one
-- The store's drafts when the token is one of its live preview links.
SELECT d.theme, d.home
FROM storefront_previews p
LEFT JOIN storefront_drafts d ON d.store_id = p.store_id
WHERE p.token_hash = @token_hash AND p.store_id = @store_id AND p.expires_at > now();
